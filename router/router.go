package router

import (
	"sort"
	"strings"
	"sync"

	"github.com/localrivet/liteproxy/compose"
)

// Router holds the routing table with thread-safe access
type Router struct {
	mu        sync.RWMutex
	routes    []compose.Route           // exact host routes (sorted by path length)
	wildcards []compose.Route           // wildcard host routes (*.example.com)
	redirects map[string]*compose.Route // redirect domain → target route
}

// New creates a new Router from a list of routes
func New(routes []compose.Route) *Router {
	r := &Router{
		redirects: make(map[string]*compose.Route),
	}
	r.Update(routes)
	return r
}

// Update replaces the routing table with new routes
func (r *Router) Update(routes []compose.Route) {
	r.mu.Lock()
	defer r.mu.Unlock()

	// Separate exact and wildcard routes
	var exact, wildcards []compose.Route
	for _, route := range routes {
		if strings.HasPrefix(route.Host, "*.") {
			wildcards = append(wildcards, route)
		} else {
			exact = append(exact, route)
		}
	}

	// Sort both by path length descending (longest prefix first)
	sort.Slice(exact, func(i, j int) bool {
		return len(exact[i].PathPrefix) > len(exact[j].PathPrefix)
	})
	sort.Slice(wildcards, func(i, j int) bool {
		return len(wildcards[i].PathPrefix) > len(wildcards[j].PathPrefix)
	})

	r.routes = exact
	r.wildcards = wildcards

	// Build redirect map from all routes
	r.redirects = make(map[string]*compose.Route)
	for i := range r.routes {
		route := &r.routes[i]
		for _, domain := range route.RedirectFrom {
			r.redirects[domain] = route
		}
	}
	for i := range r.wildcards {
		route := &r.wildcards[i]
		for _, domain := range route.RedirectFrom {
			r.redirects[domain] = route
		}
	}
}

// Match finds the route for a request using longest prefix matching
// Priority: exact host match > wildcard host match
// Returns nil if no route matches
func (r *Router) Match(host, path string) *compose.Route {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Strip port from host if present
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	// Normalize empty path to /
	if path == "" {
		path = "/"
	}

	// Try exact host match first
	for i := range r.routes {
		route := &r.routes[i]
		if route.Host != host {
			continue
		}
		if matchesPathPrefix(path, route.PathPrefix) {
			return route
		}
	}

	// Try wildcard match (*.example.com)
	if idx := strings.Index(host, "."); idx != -1 {
		wildcardHost := "*" + host[idx:] // "acme.tenant.com" → "*.tenant.com"
		for i := range r.wildcards {
			route := &r.wildcards[i]
			if route.Host != wildcardHost {
				continue
			}
			if matchesPathPrefix(path, route.PathPrefix) {
				return route
			}
		}
	}

	return nil
}

// matchesPathPrefix checks if path matches the prefix with proper path boundary handling
// e.g., /api matches /api, /api/, /api/users but NOT /apiv2
func matchesPathPrefix(path, prefix string) bool {
	if !strings.HasPrefix(path, prefix) {
		return false
	}
	// If prefix is / or ends with /, the HasPrefix check is sufficient
	if prefix == "/" || strings.HasSuffix(prefix, "/") {
		return true
	}
	// Check that the match is at a path boundary
	// Either exact match, or followed by /
	if len(path) == len(prefix) {
		return true // exact match
	}
	// path is longer than prefix, check next char is /
	return path[len(prefix)] == '/'
}

// Redirect checks if the host should redirect, returns target route or nil
func (r *Router) Redirect(host string) *compose.Route {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Strip port from host if present
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	return r.redirects[host]
}

// Hosts returns all unique hosts that should be served (for TLS certificates)
// Wildcard hosts are returned as-is (e.g., "*.tenant.com")
func (r *Router) Hosts() []string {
	r.mu.RLock()
	defer r.mu.RUnlock()

	hostSet := make(map[string]struct{})
	for _, route := range r.routes {
		hostSet[route.Host] = struct{}{}
		for _, redirect := range route.RedirectFrom {
			hostSet[redirect] = struct{}{}
		}
	}
	for _, route := range r.wildcards {
		hostSet[route.Host] = struct{}{}
		for _, redirect := range route.RedirectFrom {
			hostSet[redirect] = struct{}{}
		}
	}

	hosts := make([]string, 0, len(hostSet))
	for host := range hostSet {
		hosts = append(hosts, host)
	}
	sort.Strings(hosts)
	return hosts
}

// Routes returns a copy of all routes (for debugging/logging)
func (r *Router) Routes() []compose.Route {
	r.mu.RLock()
	defer r.mu.RUnlock()

	routes := make([]compose.Route, 0, len(r.routes)+len(r.wildcards))
	routes = append(routes, r.routes...)
	routes = append(routes, r.wildcards...)
	return routes
}

// GetPassthrough returns the passthrough route for a host, or nil if not passthrough
func (r *Router) GetPassthrough(host string) *compose.Route {
	return r.getPassthroughRoute(host)
}

// GetPassthroughPort returns the appropriate port for passthrough based on protocol
// For HTTP, returns HTTPPort if set, otherwise ServicePort
// For HTTPS (forHTTP=false), always returns ServicePort
func (r *Router) GetPassthroughPort(host string, forHTTP bool) (route *compose.Route, port int) {
	route = r.getPassthroughRoute(host)
	if route == nil {
		return nil, 0
	}
	if forHTTP && route.HTTPPort > 0 {
		return route, route.HTTPPort
	}
	return route, route.ServicePort
}

func (r *Router) getPassthroughRoute(host string) *compose.Route {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Strip port from host if present
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	// Check exact matches first
	for i := range r.routes {
		route := &r.routes[i]
		if route.Host == host && route.Passthrough {
			return route
		}
	}

	// Check wildcard matches
	if idx := strings.Index(host, "."); idx != -1 {
		wildcardHost := "*" + host[idx:]
		for i := range r.wildcards {
			route := &r.wildcards[i]
			if route.Host == wildcardHost && route.Passthrough {
				return route
			}
		}
	}

	return nil
}

// HasPassthroughRoutes returns true if any routes have TLS passthrough enabled
func (r *Router) HasPassthroughRoutes() bool {
	r.mu.RLock()
	defer r.mu.RUnlock()

	for i := range r.routes {
		if r.routes[i].Passthrough {
			return true
		}
	}
	for i := range r.wildcards {
		if r.wildcards[i].Passthrough {
			return true
		}
	}
	return false
}
