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
	routes    []compose.Route
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

	// Sort routes by path length descending (longest prefix first)
	sorted := make([]compose.Route, len(routes))
	copy(sorted, routes)
	sort.Slice(sorted, func(i, j int) bool {
		return len(sorted[i].PathPrefix) > len(sorted[j].PathPrefix)
	})
	r.routes = sorted

	// Build redirect map
	r.redirects = make(map[string]*compose.Route)
	for i := range r.routes {
		route := &r.routes[i]
		for _, domain := range route.RedirectFrom {
			r.redirects[domain] = route
		}
	}
}

// Match finds the route for a request using longest prefix matching
// Returns nil if no route matches
func (r *Router) Match(host, path string) *compose.Route {
	r.mu.RLock()
	defer r.mu.RUnlock()

	// Strip port from host if present
	if idx := strings.LastIndex(host, ":"); idx != -1 {
		host = host[:idx]
	}

	for i := range r.routes {
		route := &r.routes[i]
		if route.Host == host && strings.HasPrefix(path, route.PathPrefix) {
			return route
		}
	}
	return nil
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

	routes := make([]compose.Route, len(r.routes))
	copy(routes, r.routes)
	return routes
}
