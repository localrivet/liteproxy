package proxy

import (
	"fmt"
	"log"
	"net"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"sync/atomic"
	"time"

	"github.com/localrivet/liteproxy/compose"
	"github.com/localrivet/liteproxy/router"
)

// bufferPool implements httputil.BufferPool for efficient memory reuse
type bufferPool struct {
	pool sync.Pool
}

const bufferSize = 32 * 1024 // 32KB, same as Traefik

func newBufferPool() *bufferPool {
	return &bufferPool{
		pool: sync.Pool{
			New: func() any {
				return make([]byte, bufferSize)
			},
		},
	}
}

func (b *bufferPool) Get() []byte {
	return b.pool.Get().([]byte)
}

func (b *bufferPool) Put(buf []byte) {
	b.pool.Put(buf)
}

// Shared resources for all proxies
var (
	sharedBufferPool = newBufferPool()
	sharedTransport = &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
)

// Handler serves as the main HTTP handler for proxying requests
type Handler struct {
	router atomic.Pointer[router.Router] // lock-free router access
	scheme string                        // http or https for redirects

	mu      sync.RWMutex
	proxies map[string]*httputil.ReverseProxy // cache of proxies by service:port
}

// New creates a new proxy Handler
func New(r *router.Router, scheme string) *Handler {
	h := &Handler{
		scheme:  scheme,
		proxies: make(map[string]*httputil.ReverseProxy),
	}
	h.router.Store(r)
	return h
}

// UpdateRouter updates the router (called on config reload)
func (h *Handler) UpdateRouter(r *router.Router) {
	h.router.Store(r) // atomic, lock-free

	// Clear proxy cache under lock
	h.mu.Lock()
	h.proxies = make(map[string]*httputil.ReverseProxy)
	h.mu.Unlock()
}

// ServeHTTP handles incoming requests
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	path := r.URL.Path

	// Get router atomically (lock-free)
	rtr := h.router.Load()

	// Check for redirect first
	if target := rtr.Redirect(host); target != nil {
		redirectURL := fmt.Sprintf("%s://%s%s", h.scheme, target.Host, path)
		if r.URL.RawQuery != "" {
			redirectURL += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, redirectURL, http.StatusMovedPermanently)
		return
	}

	// Find matching route
	route := rtr.Match(host, path)
	if route == nil {
		http.Error(w, "no route found", http.StatusNotFound)
		return
	}

	// Get or create proxy for this route
	proxy := h.getProxy(route)

	// Strip the path prefix before proxying (if enabled)
	if route.StripPrefix && route.PathPrefix != "/" {
		r.URL.Path = strings.TrimPrefix(r.URL.Path, route.PathPrefix)
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}
	}

	proxy.ServeHTTP(w, r)
}

// getProxy returns a cached or new reverse proxy for the route
func (h *Handler) getProxy(route *compose.Route) *httputil.ReverseProxy {
	key := fmt.Sprintf("%s:%d", route.ServiceName, route.ServicePort)

	h.mu.RLock()
	proxy, ok := h.proxies[key]
	h.mu.RUnlock()
	if ok {
		return proxy
	}

	// Create new proxy
	h.mu.Lock()
	defer h.mu.Unlock()

	// Double-check after acquiring write lock
	if proxy, ok := h.proxies[key]; ok {
		return proxy
	}

	target := &url.URL{
		Scheme: "http",
		Host:   fmt.Sprintf("%s:%d", route.ServiceName, route.ServicePort),
	}

	proxy = h.buildProxy(target, route.PassHostHeader)
	h.proxies[key] = proxy
	return proxy
}

// buildProxy creates a high-performance reverse proxy
func (h *Handler) buildProxy(target *url.URL, passHostHeader bool) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)

			if passHostHeader {
				pr.Out.Host = pr.In.Host
			}

			// Normalize WebSocket headers for strict servers
			normalizeWebSocketHeaders(pr.Out.Header)

			pr.SetXForwarded()
		},

		Transport:     sharedTransport,
		FlushInterval: 100 * time.Millisecond,
		BufferPool:    sharedBufferPool,

		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("proxy error to %s: %v", target.Host, err)
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, "Bad Gateway: %v", err)
		},
	}
}

// normalizeWebSocketHeaders ensures WebSocket headers have correct casing
// Some strict WebSocket servers require exact header names
func normalizeWebSocketHeaders(h http.Header) {
	// Standard WebSocket headers that need normalization
	wsHeaders := []string{
		"Sec-Websocket-Key",
		"Sec-Websocket-Version",
		"Sec-Websocket-Protocol",
		"Sec-Websocket-Extensions",
		"Sec-Websocket-Accept",
	}

	for _, name := range wsHeaders {
		if values, ok := h[name]; ok {
			delete(h, name)
			// Use canonical form: Sec-WebSocket-*
			canonical := strings.Replace(name, "Websocket", "WebSocket", 1)
			h[canonical] = values
		}
	}
}
