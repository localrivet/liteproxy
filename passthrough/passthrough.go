package passthrough

import (
	"bufio"
	"crypto/tls"
	"fmt"
	"io"
	"net"
	"net/http"
	"sync"
	"time"

	"github.com/localrivet/liteproxy/router"
)

const (
	peekBufSize = 4096
	copyBufSize = 32 * 1024 // 32KB - same as proxy handler
)

// Shared buffer pools for zero-allocation hot path
var (
	peekBufPool = sync.Pool{New: func() any { return make([]byte, peekBufSize) }}
	copyBufPool = sync.Pool{New: func() any { return make([]byte, copyBufSize) }}
)

// Listener wraps a net.Listener and routes connections based on SNI/Host
type Listener struct {
	net.Listener
	router       *router.Router
	httpHandler  http.Handler
	httpsHandler http.Handler
	tlsConfig    *tls.Config
	isTLS        bool

	mu sync.RWMutex
}

// NewTLSListener creates a listener that peeks SNI for passthrough routing
func NewTLSListener(ln net.Listener, r *router.Router, httpsHandler http.Handler, tlsConfig *tls.Config) *Listener {
	return &Listener{
		Listener:     ln,
		router:       r,
		httpsHandler: httpsHandler,
		tlsConfig:    tlsConfig,
		isTLS:        true,
	}
}

// NewHTTPListener creates a listener that peeks Host header for passthrough routing
func NewHTTPListener(ln net.Listener, r *router.Router, httpHandler http.Handler) *Listener {
	return &Listener{
		Listener:    ln,
		router:      r,
		httpHandler: httpHandler,
		isTLS:       false,
	}
}

// UpdateRouter updates the router (called on config reload)
func (l *Listener) UpdateRouter(r *router.Router) {
	l.mu.Lock()
	defer l.mu.Unlock()
	l.router = r
}

// Serve accepts connections and routes them appropriately
func (l *Listener) Serve() error {
	for {
		conn, err := l.Accept()
		if err != nil {
			return err
		}
		go l.handleConn(conn)
	}
}

func (l *Listener) handleConn(conn net.Conn) {
	l.mu.RLock()
	r := l.router
	l.mu.RUnlock()

	if l.isTLS {
		l.handleTLSConn(conn, r)
	} else {
		l.handleHTTPConn(conn, r)
	}
}

func (l *Listener) handleTLSConn(conn net.Conn, r *router.Router) {
	// Get buffer from pool
	buf := peekBufPool.Get().([]byte)

	// Peek at TLS ClientHello to extract SNI
	n, err := conn.Read(buf)
	if err != nil {
		peekBufPool.Put(buf)
		conn.Close()
		return
	}

	sni, err := extractSNI(buf[:n])
	if err != nil {
		// Not valid TLS or no SNI - close connection
		peekBufPool.Put(buf)
		conn.Close()
		return
	}

	// Check if this host needs passthrough
	route := r.GetPassthrough(sni)
	if route != nil {
		// Passthrough: forward raw TCP to backend
		backend := fmt.Sprintf("%s:%d", route.ServiceName, route.ServicePort)
		proxyTCP(conn, backend, buf[:n])
		peekBufPool.Put(buf)
		return
	}

	// Not passthrough: do TLS termination and serve via HTTPS handler
	// Create replay connection with peeked data, then wrap with TLS
	wrappedConn := &replayConn{Conn: conn, buf: buf[:n], pool: &peekBufPool, poolBuf: buf}
	tlsConn := tls.Server(wrappedConn, l.tlsConfig)
	server := &http.Server{Handler: l.httpsHandler}
	singleLn := newSingleConnListener(tlsConn)
	server.Serve(singleLn)
}

func (l *Listener) handleHTTPConn(conn net.Conn, r *router.Router) {
	// Get buffer from pool
	buf := peekBufPool.Get().([]byte)

	// Peek at HTTP request for Host header
	n, err := conn.Read(buf)
	if err != nil {
		peekBufPool.Put(buf)
		conn.Close()
		return
	}

	host, err := extractHTTPHost(buf[:n])
	if err != nil {
		peekBufPool.Put(buf)
		conn.Close()
		return
	}

	// Check if this host needs passthrough (use HTTP port if configured)
	route, port := r.GetPassthroughPort(host, true)
	if route != nil {
		// Passthrough: forward raw TCP to backend (using http_port if set)
		backend := fmt.Sprintf("%s:%d", route.ServiceName, port)
		proxyTCP(conn, backend, buf[:n])
		peekBufPool.Put(buf)
		return
	}

	// Not passthrough: serve via HTTP handler
	wrappedConn := &replayConn{Conn: conn, buf: buf[:n], pool: &peekBufPool, poolBuf: buf}
	server := &http.Server{Handler: l.httpHandler}
	singleLn := newSingleConnListener(wrappedConn)
	server.Serve(singleLn)
}

// proxyTCP forwards raw TCP between client and backend with zero-copy where possible
func proxyTCP(client net.Conn, backend string, initialData []byte) {
	backendConn, err := net.DialTimeout("tcp", backend, 10*time.Second)
	if err != nil {
		client.Close()
		return
	}

	// Write peeked data to backend first
	if len(initialData) > 0 {
		if _, err := backendConn.Write(initialData); err != nil {
			client.Close()
			backendConn.Close()
			return
		}
	}

	// Bidirectional copy with pooled buffers
	var wg sync.WaitGroup
	wg.Add(2)

	// Client → Backend
	go func() {
		buf := copyBufPool.Get().([]byte)
		io.CopyBuffer(backendConn, client, buf)
		copyBufPool.Put(buf)
		if tc, ok := backendConn.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		wg.Done()
	}()

	// Backend → Client
	go func() {
		buf := copyBufPool.Get().([]byte)
		io.CopyBuffer(client, backendConn, buf)
		copyBufPool.Put(buf)
		if tc, ok := client.(*net.TCPConn); ok {
			tc.CloseWrite()
		}
		wg.Done()
	}()

	wg.Wait()
	client.Close()
	backendConn.Close()
}

// replayConn replays buffered data before reading from underlying conn
type replayConn struct {
	net.Conn
	buf     []byte
	pool    *sync.Pool
	poolBuf []byte // original buffer to return to pool
}

func (c *replayConn) Read(b []byte) (int, error) {
	if len(c.buf) > 0 {
		n := copy(b, c.buf)
		c.buf = c.buf[n:]
		if len(c.buf) == 0 && c.pool != nil && c.poolBuf != nil {
			c.pool.Put(c.poolBuf)
			c.pool = nil
			c.poolBuf = nil
		}
		return n, nil
	}
	return c.Conn.Read(b)
}

func (c *replayConn) Close() error {
	if c.pool != nil && c.poolBuf != nil {
		c.pool.Put(c.poolBuf)
		c.pool = nil
		c.poolBuf = nil
	}
	return c.Conn.Close()
}

// singleConnListener serves exactly one connection then returns EOF
type singleConnListener struct {
	conn   net.Conn
	served int32
}

func newSingleConnListener(conn net.Conn) *singleConnListener {
	return &singleConnListener{conn: conn}
}

func (l *singleConnListener) Accept() (net.Conn, error) {
	if l.served > 0 {
		return nil, net.ErrClosed
	}
	l.served = 1
	return l.conn, nil
}

func (l *singleConnListener) Close() error   { return nil }
func (l *singleConnListener) Addr() net.Addr { return l.conn.LocalAddr() }

// extractSNI parses TLS ClientHello and returns the SNI hostname
func extractSNI(data []byte) (string, error) {
	if len(data) < 5 {
		return "", fmt.Errorf("too short")
	}

	// TLS record: ContentType(1) + Version(2) + Length(2)
	if data[0] != 0x16 { // Handshake
		return "", fmt.Errorf("not TLS handshake")
	}

	recordLen := int(data[3])<<8 | int(data[4])
	if len(data) < 5+recordLen {
		recordLen = len(data) - 5 // Work with what we have
	}

	// Handshake: Type(1) + Length(3) + ...
	pos := 5
	if pos >= len(data) || data[pos] != 0x01 { // ClientHello
		return "", fmt.Errorf("not ClientHello")
	}
	pos += 4 // type + length

	// ClientHello: Version(2) + Random(32) + SessionID(1+n) + CipherSuites(2+n) + Compression(1+n) + Extensions(2+n)
	if pos+2 > len(data) {
		return "", fmt.Errorf("truncated")
	}
	pos += 2 // version

	if pos+32 > len(data) {
		return "", fmt.Errorf("truncated")
	}
	pos += 32 // random

	if pos+1 > len(data) {
		return "", fmt.Errorf("truncated")
	}
	sessionIDLen := int(data[pos])
	pos += 1 + sessionIDLen

	if pos+2 > len(data) {
		return "", fmt.Errorf("truncated")
	}
	cipherSuitesLen := int(data[pos])<<8 | int(data[pos+1])
	pos += 2 + cipherSuitesLen

	if pos+1 > len(data) {
		return "", fmt.Errorf("truncated")
	}
	compressionLen := int(data[pos])
	pos += 1 + compressionLen

	if pos+2 > len(data) {
		return "", fmt.Errorf("truncated")
	}
	extensionsLen := int(data[pos])<<8 | int(data[pos+1])
	pos += 2

	end := pos + extensionsLen
	if end > len(data) {
		end = len(data)
	}

	// Parse extensions looking for SNI (type 0x0000)
	for pos+4 <= end {
		extType := int(data[pos])<<8 | int(data[pos+1])
		extLen := int(data[pos+2])<<8 | int(data[pos+3])
		pos += 4

		if extType == 0 && pos+extLen <= end { // SNI extension
			if pos+2 > end {
				break
			}
			pos += 2 // SNI list length

			if pos+3 > end {
				break
			}
			nameType := data[pos]
			nameLen := int(data[pos+1])<<8 | int(data[pos+2])
			pos += 3

			if nameType == 0 && pos+nameLen <= end {
				return string(data[pos : pos+nameLen]), nil
			}
			break
		}
		pos += extLen
	}

	return "", fmt.Errorf("no SNI")
}

// extractHTTPHost parses HTTP request and returns Host header
func extractHTTPHost(data []byte) (string, error) {
	reader := bufio.NewReader(&bytesReader{data, 0})
	req, err := http.ReadRequest(reader)
	if err != nil {
		return "", err
	}
	return req.Host, nil
}

type bytesReader struct {
	buf []byte
	pos int
}

func (r *bytesReader) Read(b []byte) (int, error) {
	if r.pos >= len(r.buf) {
		return 0, io.EOF
	}
	n := copy(b, r.buf[r.pos:])
	r.pos += n
	return n, nil
}
