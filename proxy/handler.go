package proxy

import (
	"fmt"
	"log"
	"net/http"
	"net/http/httputil"
	"net/url"
	"strings"
	"sync"
	"time"

	"github.com/localrivet/liteproxy/compose"
	"github.com/localrivet/liteproxy/router"
)

// Handler serves as the main HTTP handler for proxying requests
type Handler struct {
	router *router.Router
	scheme string // http or https for redirects

	mu      sync.RWMutex
	proxies map[string]*httputil.ReverseProxy // cache of proxies by service:port
}

// New creates a new proxy Handler
func New(r *router.Router, scheme string) *Handler {
	return &Handler{
		router:  r,
		scheme:  scheme,
		proxies: make(map[string]*httputil.ReverseProxy),
	}
}

// UpdateRouter updates the router (called on config reload)
func (h *Handler) UpdateRouter(r *router.Router) {
	h.mu.Lock()
	defer h.mu.Unlock()
	h.router = r
	h.proxies = make(map[string]*httputil.ReverseProxy) // clear proxy cache
}

// ServeHTTP handles incoming requests
func (h *Handler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	path := r.URL.Path

	// Check for redirect first
	if target := h.router.Redirect(host); target != nil {
		redirectURL := fmt.Sprintf("%s://%s%s", h.scheme, target.Host, path)
		if r.URL.RawQuery != "" {
			redirectURL += "?" + r.URL.RawQuery
		}
		http.Redirect(w, r, redirectURL, http.StatusMovedPermanently)
		return
	}

	// Find matching route
	route := h.router.Match(host, path)
	if route == nil {
		http.Error(w, "no route found", http.StatusNotFound)
		return
	}

	// Get or create proxy for this route
	proxy := h.getProxy(route)

	// Strip the path prefix before proxying
	// e.g., /api/users with prefix /api becomes /users
	if route.PathPrefix != "/" {
		originalPath := r.URL.Path
		r.URL.Path = strings.TrimPrefix(r.URL.Path, route.PathPrefix)
		if r.URL.Path == "" {
			r.URL.Path = "/"
		}
		// Also update RawPath if set
		if r.URL.RawPath != "" {
			r.URL.RawPath = strings.TrimPrefix(r.URL.RawPath, route.PathPrefix)
			if r.URL.RawPath == "" {
				r.URL.RawPath = "/"
			}
		}
		log.Printf("proxy: %s%s -> %s:%d%s", host, originalPath, route.ServiceName, route.ServicePort, r.URL.Path)
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

// buildProxy creates a reverse proxy following Traefik-style patterns
func (h *Handler) buildProxy(target *url.URL, passHostHeader bool) *httputil.ReverseProxy {
	return &httputil.ReverseProxy{
		Rewrite: func(pr *httputil.ProxyRequest) {
			pr.SetURL(target)

			if passHostHeader {
				pr.Out.Host = pr.In.Host
			}

			pr.SetXForwarded()
		},

		FlushInterval: 100 * time.Millisecond,

		ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
			log.Printf("proxy error to %s: %v", target.Host, err)
			w.WriteHeader(http.StatusBadGateway)
			fmt.Fprintf(w, "Bad Gateway: %v", err)
		},
	}
}
