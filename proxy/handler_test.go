package proxy

import (
	"io"
	"net/http"
	"net/http/httptest"
	"net/http/httputil"
	"net/url"
	"testing"
	"time"

	"github.com/localrivet/liteproxy/compose"
	"github.com/localrivet/liteproxy/router"
)

func TestRedirect(t *testing.T) {
	routes := []compose.Route{
		{
			Host:         "example.com",
			PathPrefix:   "/",
			ServiceName:  "web",
			ServicePort:  80,
			RedirectFrom: []string{"www.example.com", "old.example.com"},
		},
	}
	r := router.New(routes)
	h := New(r, "https")

	tests := []struct {
		name         string
		host         string
		path         string
		query        string
		wantCode     int
		wantLocation string
	}{
		{
			name:         "redirect www to primary",
			host:         "www.example.com",
			path:         "/",
			wantCode:     http.StatusMovedPermanently,
			wantLocation: "https://example.com/",
		},
		{
			name:         "redirect preserves path",
			host:         "www.example.com",
			path:         "/about",
			wantCode:     http.StatusMovedPermanently,
			wantLocation: "https://example.com/about",
		},
		{
			name:         "redirect preserves query string",
			host:         "www.example.com",
			path:         "/search",
			query:        "q=test",
			wantCode:     http.StatusMovedPermanently,
			wantLocation: "https://example.com/search?q=test",
		},
		{
			name:         "redirect old domain",
			host:         "old.example.com",
			path:         "/page",
			wantCode:     http.StatusMovedPermanently,
			wantLocation: "https://example.com/page",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			urlStr := "http://" + tt.host + tt.path
			if tt.query != "" {
				urlStr += "?" + tt.query
			}
			req := httptest.NewRequest("GET", urlStr, nil)
			req.Host = tt.host
			w := httptest.NewRecorder()

			h.ServeHTTP(w, req)

			if w.Code != tt.wantCode {
				t.Errorf("status = %d, want %d", w.Code, tt.wantCode)
			}
			if loc := w.Header().Get("Location"); loc != tt.wantLocation {
				t.Errorf("Location = %q, want %q", loc, tt.wantLocation)
			}
		})
	}
}

func TestRedirectHTTPScheme(t *testing.T) {
	routes := []compose.Route{
		{
			Host:         "example.com",
			PathPrefix:   "/",
			ServiceName:  "web",
			ServicePort:  80,
			RedirectFrom: []string{"www.example.com"},
		},
	}
	r := router.New(routes)
	h := New(r, "http") // HTTP scheme

	req := httptest.NewRequest("GET", "http://www.example.com/page", nil)
	req.Host = "www.example.com"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	wantLocation := "http://example.com/page"
	if loc := w.Header().Get("Location"); loc != wantLocation {
		t.Errorf("Location = %q, want %q", loc, wantLocation)
	}
}

func TestNoRouteFound(t *testing.T) {
	routes := []compose.Route{
		{Host: "example.com", PathPrefix: "/", ServiceName: "web", ServicePort: 80},
	}
	r := router.New(routes)
	h := New(r, "https")

	req := httptest.NewRequest("GET", "http://unknown.com/", nil)
	req.Host = "unknown.com"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusNotFound {
		t.Errorf("status = %d, want %d", w.Code, http.StatusNotFound)
	}
}

func TestPathStrippingLogic(t *testing.T) {
	tests := []struct {
		name        string
		pathPrefix  string
		stripPrefix bool
		requestPath string
		wantPath    string
	}{
		{
			name:        "strip prefix enabled",
			pathPrefix:  "/api",
			stripPrefix: true,
			requestPath: "/api/users",
			wantPath:    "/users",
		},
		{
			name:        "strip prefix - root becomes /",
			pathPrefix:  "/api",
			stripPrefix: true,
			requestPath: "/api",
			wantPath:    "/",
		},
		{
			name:        "strip prefix disabled",
			pathPrefix:  "/api",
			stripPrefix: false,
			requestPath: "/api/users",
			wantPath:    "/api/users",
		},
		{
			name:        "root prefix - no stripping needed",
			pathPrefix:  "/",
			stripPrefix: true,
			requestPath: "/users",
			wantPath:    "/users",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			routes := []compose.Route{
				{
					Host:        "example.com",
					PathPrefix:  tt.pathPrefix,
					ServiceName: "backend",
					ServicePort: 80,
					StripPrefix: tt.stripPrefix,
				},
			}

			r := router.New(routes)
			route := r.Match("example.com", tt.requestPath)
			if route == nil {
				t.Fatal("route not found")
			}

			// Simulate path stripping logic from handler
			testPath := tt.requestPath
			if route.StripPrefix && route.PathPrefix != "/" {
				testPath = testPath[len(route.PathPrefix):]
				if testPath == "" {
					testPath = "/"
				}
			}

			if testPath != tt.wantPath {
				t.Errorf("path after stripping = %q, want %q", testPath, tt.wantPath)
			}
		})
	}
}

func TestProxyIntegration(t *testing.T) {
	// Create a real backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Received-Path", r.URL.Path)
		w.Header().Set("X-Received-Host", r.Host)
		io.WriteString(w, "backend response")
	}))
	defer backend.Close()

	// Parse the backend URL
	backendURL, _ := url.Parse(backend.URL)

	// Create a handler with a custom proxy that points to our test backend
	routes := []compose.Route{
		{
			Host:        "example.com",
			PathPrefix:  "/api",
			ServiceName: "api",
			ServicePort: 8080,
			StripPrefix: true,
		},
	}
	rtr := router.New(routes)
	h := New(rtr, "http")

	// Pre-populate the proxy cache with our test backend
	h.proxies["api:8080"] = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(backendURL)
			pr.SetXForwarded()
		},
		FlushInterval: 100 * time.Millisecond,
	}

	// Test proxying
	req := httptest.NewRequest("GET", "http://example.com/api/users", nil)
	req.Host = "example.com"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	if w.Code != http.StatusOK {
		t.Errorf("status = %d, want %d", w.Code, http.StatusOK)
	}

	body := w.Body.String()
	if body != "backend response" {
		t.Errorf("body = %q, want %q", body, "backend response")
	}

	// Check that path was stripped
	receivedPath := w.Header().Get("X-Received-Path")
	if receivedPath != "/users" {
		t.Errorf("backend received path = %q, want %q", receivedPath, "/users")
	}
}

func TestProxyIntegrationNoStrip(t *testing.T) {
	// Create a real backend server
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Received-Path", r.URL.Path)
		io.WriteString(w, "ok")
	}))
	defer backend.Close()

	backendURL, _ := url.Parse(backend.URL)

	routes := []compose.Route{
		{
			Host:        "example.com",
			PathPrefix:  "/api",
			ServiceName: "api",
			ServicePort: 8080,
			StripPrefix: false, // Don't strip
		},
	}
	rtr := router.New(routes)
	h := New(rtr, "http")

	h.proxies["api:8080"] = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(backendURL)
			pr.SetXForwarded()
		},
		FlushInterval: 100 * time.Millisecond,
	}

	req := httptest.NewRequest("GET", "http://example.com/api/users", nil)
	req.Host = "example.com"
	w := httptest.NewRecorder()

	h.ServeHTTP(w, req)

	// Path should NOT be stripped
	receivedPath := w.Header().Get("X-Received-Path")
	if receivedPath != "/api/users" {
		t.Errorf("backend received path = %q, want %q", receivedPath, "/api/users")
	}
}

func TestUpdateRouter(t *testing.T) {
	routes1 := []compose.Route{
		{Host: "old.com", PathPrefix: "/", ServiceName: "old", ServicePort: 80},
	}
	r1 := router.New(routes1)
	h := New(r1, "https")

	// new.com should 404 before update
	req := httptest.NewRequest("GET", "http://new.com/", nil)
	req.Host = "new.com"
	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)
	if w.Code != http.StatusNotFound {
		t.Errorf("before update, new.com status = %d, want %d", w.Code, http.StatusNotFound)
	}

	// Update router
	routes2 := []compose.Route{
		{Host: "new.com", PathPrefix: "/", ServiceName: "new", ServicePort: 80},
	}
	r2 := router.New(routes2)
	h.UpdateRouter(r2)

	// Now old.com should 404
	req2 := httptest.NewRequest("GET", "http://old.com/", nil)
	req2.Host = "old.com"
	w2 := httptest.NewRecorder()
	h.ServeHTTP(w2, req2)
	if w2.Code != http.StatusNotFound {
		t.Errorf("after update, old.com status = %d, want %d", w2.Code, http.StatusNotFound)
	}
}

func TestHandlerNew(t *testing.T) {
	routes := []compose.Route{
		{Host: "example.com", PathPrefix: "/", ServiceName: "web", ServicePort: 80},
	}
	r := router.New(routes)
	h := New(r, "https")

	if h.router.Load() == nil {
		t.Error("handler.router is nil")
	}
	if h.scheme != "https" {
		t.Errorf("handler.scheme = %q, want %q", h.scheme, "https")
	}
	if h.proxies == nil {
		t.Error("handler.proxies is nil")
	}
}

func TestNormalizeWebSocketHeaders(t *testing.T) {
	tests := []struct {
		name     string
		input    string
		expected string
	}{
		{"key", "Sec-Websocket-Key", "Sec-WebSocket-Key"},
		{"version", "Sec-Websocket-Version", "Sec-WebSocket-Version"},
		{"protocol", "Sec-Websocket-Protocol", "Sec-WebSocket-Protocol"},
		{"extensions", "Sec-Websocket-Extensions", "Sec-WebSocket-Extensions"},
		{"accept", "Sec-Websocket-Accept", "Sec-WebSocket-Accept"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			h := http.Header{}
			// Set directly to simulate canonicalized header from incoming request
			h[tt.input] = []string{"test-value"}

			normalizeWebSocketHeaders(h)

			// Check map directly (h.Get would re-canonicalize)
			if values, exists := h[tt.expected]; !exists || values[0] != "test-value" {
				t.Errorf("header %q not normalized to %q, got map: %v", tt.input, tt.expected, h)
			}
			// Original should be removed
			if _, exists := h[tt.input]; exists {
				t.Errorf("original header %q should be removed", tt.input)
			}
		})
	}
}

func TestWebSocketHeadersForwarded(t *testing.T) {
	// Track what headers the backend receives
	var receivedHeaders http.Header

	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		receivedHeaders = r.Header.Clone()
		// Return 200 instead of 101 since httptest can't do WebSocket upgrade
		w.WriteHeader(http.StatusOK)
	}))
	defer backend.Close()

	backendURL, _ := url.Parse(backend.URL)

	routes := []compose.Route{
		{Host: "example.com", PathPrefix: "/ws", ServiceName: "ws", ServicePort: 8080, StripPrefix: true},
	}
	rtr := router.New(routes)
	h := New(rtr, "http")

	h.proxies["ws:8080"] = &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(backendURL)
			normalizeWebSocketHeaders(pr.Out.Header)
			pr.SetXForwarded()
		},
		Transport:     sharedTransport,
		FlushInterval: 100 * time.Millisecond,
		BufferPool:    sharedBufferPool,
	}

	req := httptest.NewRequest("GET", "http://example.com/ws", nil)
	req.Host = "example.com"
	req.Header.Set("Upgrade", "websocket")
	req.Header.Set("Connection", "Upgrade")
	req.Header["Sec-Websocket-Key"] = []string{"dGhlIHNhbXBsZSBub25jZQ=="}
	req.Header["Sec-Websocket-Version"] = []string{"13"}

	w := httptest.NewRecorder()
	h.ServeHTTP(w, req)

	// Verify WebSocket headers were forwarded (and normalized)
	if receivedHeaders.Get("Upgrade") != "websocket" {
		t.Error("Upgrade header not forwarded")
	}
	// Check normalized header exists (may be under either key due to Go's handling)
	if _, ok := receivedHeaders["Sec-WebSocket-Key"]; !ok {
		if _, ok := receivedHeaders["Sec-Websocket-Key"]; !ok {
			t.Error("Sec-WebSocket-Key header not forwarded")
		}
	}
}
