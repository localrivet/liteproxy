package router

import (
	"testing"

	"github.com/localrivet/liteproxy/compose"
)

func TestMatch(t *testing.T) {
	routes := []compose.Route{
		{Host: "example.com", PathPrefix: "/", ServiceName: "web", ServicePort: 80},
		{Host: "example.com", PathPrefix: "/api", ServiceName: "api", ServicePort: 8080},
		{Host: "example.com", PathPrefix: "/api/v2", ServiceName: "api-v2", ServicePort: 8081},
		{Host: "other.com", PathPrefix: "/", ServiceName: "other", ServicePort: 80},
	}
	r := New(routes)

	tests := []struct {
		name        string
		host        string
		path        string
		wantService string
		wantNil     bool
	}{
		{
			name:        "root path",
			host:        "example.com",
			path:        "/",
			wantService: "web",
		},
		{
			name:        "static page",
			host:        "example.com",
			path:        "/about",
			wantService: "web",
		},
		{
			name:        "api path",
			host:        "example.com",
			path:        "/api",
			wantService: "api",
		},
		{
			name:        "api subpath",
			host:        "example.com",
			path:        "/api/users",
			wantService: "api",
		},
		{
			name:        "api v2 - longest prefix wins",
			host:        "example.com",
			path:        "/api/v2/users",
			wantService: "api-v2",
		},
		{
			name:        "different host",
			host:        "other.com",
			path:        "/",
			wantService: "other",
		},
		{
			name:    "unknown host",
			host:    "unknown.com",
			path:    "/",
			wantNil: true,
		},
		{
			name:        "host with port",
			host:        "example.com:8080",
			path:        "/api",
			wantService: "api",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := r.Match(tt.host, tt.path)
			if tt.wantNil {
				if route != nil {
					t.Errorf("Match() = %v, want nil", route)
				}
				return
			}
			if route == nil {
				t.Fatal("Match() = nil, want route")
			}
			if route.ServiceName != tt.wantService {
				t.Errorf("Match().ServiceName = %q, want %q", route.ServiceName, tt.wantService)
			}
		})
	}
}

func TestRedirect(t *testing.T) {
	routes := []compose.Route{
		{
			Host:         "example.com",
			PathPrefix:   "/",
			ServiceName:  "web",
			ServicePort:  80,
			RedirectFrom: []string{"www.example.com", "old.example.com"},
		},
		{
			Host:        "api.example.com",
			PathPrefix:  "/",
			ServiceName: "api",
			ServicePort: 8080,
		},
	}
	r := New(routes)

	tests := []struct {
		name       string
		host       string
		wantTarget string
		wantNil    bool
	}{
		{
			name:       "redirect from www",
			host:       "www.example.com",
			wantTarget: "example.com",
		},
		{
			name:       "redirect from old",
			host:       "old.example.com",
			wantTarget: "example.com",
		},
		{
			name:    "no redirect for primary host",
			host:    "example.com",
			wantNil: true,
		},
		{
			name:    "no redirect for other host",
			host:    "api.example.com",
			wantNil: true,
		},
		{
			name:    "no redirect for unknown host",
			host:    "unknown.com",
			wantNil: true,
		},
		{
			name:       "redirect with port in host",
			host:       "www.example.com:8080",
			wantTarget: "example.com",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := r.Redirect(tt.host)
			if tt.wantNil {
				if route != nil {
					t.Errorf("Redirect() = %v, want nil", route)
				}
				return
			}
			if route == nil {
				t.Fatal("Redirect() = nil, want route")
			}
			if route.Host != tt.wantTarget {
				t.Errorf("Redirect().Host = %q, want %q", route.Host, tt.wantTarget)
			}
		})
	}
}

func TestHosts(t *testing.T) {
	routes := []compose.Route{
		{
			Host:         "example.com",
			PathPrefix:   "/",
			ServiceName:  "web",
			RedirectFrom: []string{"www.example.com"},
		},
		{
			Host:        "api.example.com",
			PathPrefix:  "/",
			ServiceName: "api",
		},
	}
	r := New(routes)

	hosts := r.Hosts()
	expected := map[string]bool{
		"example.com":     true,
		"www.example.com": true,
		"api.example.com": true,
	}

	if len(hosts) != len(expected) {
		t.Errorf("Hosts() returned %d hosts, want %d", len(hosts), len(expected))
	}

	for _, h := range hosts {
		if !expected[h] {
			t.Errorf("Hosts() contains unexpected host %q", h)
		}
	}
}

func TestUpdate(t *testing.T) {
	r := New([]compose.Route{
		{Host: "old.com", PathPrefix: "/", ServiceName: "old", ServicePort: 80},
	})

	// Verify initial state
	if route := r.Match("old.com", "/"); route == nil {
		t.Fatal("initial Match() = nil, want route")
	}

	// Update routes
	r.Update([]compose.Route{
		{Host: "new.com", PathPrefix: "/", ServiceName: "new", ServicePort: 80},
	})

	// Old route should not match
	if route := r.Match("old.com", "/"); route != nil {
		t.Error("Match(old.com) after update should be nil")
	}

	// New route should match
	if route := r.Match("new.com", "/"); route == nil {
		t.Fatal("Match(new.com) after update = nil, want route")
	}
}

func TestRoutes(t *testing.T) {
	routes := []compose.Route{
		{Host: "a.com", PathPrefix: "/", ServiceName: "a", ServicePort: 80},
		{Host: "b.com", PathPrefix: "/", ServiceName: "b", ServicePort: 80},
	}
	r := New(routes)

	got := r.Routes()
	if len(got) != 2 {
		t.Errorf("Routes() returned %d routes, want 2", len(got))
	}

	// Verify it's a copy by modifying it
	got[0].Host = "modified.com"
	original := r.Routes()
	if original[0].Host == "modified.com" {
		t.Error("Routes() should return a copy, not the original slice")
	}
}

func TestLongestPrefixOrdering(t *testing.T) {
	// Add routes in random order to verify sorting works
	routes := []compose.Route{
		{Host: "example.com", PathPrefix: "/", ServiceName: "root", ServicePort: 80},
		{Host: "example.com", PathPrefix: "/a/b/c", ServiceName: "deep", ServicePort: 80},
		{Host: "example.com", PathPrefix: "/a", ServiceName: "shallow", ServicePort: 80},
		{Host: "example.com", PathPrefix: "/a/b", ServiceName: "medium", ServicePort: 80},
	}
	r := New(routes)

	tests := []struct {
		path        string
		wantService string
	}{
		{"/", "root"},
		{"/a", "shallow"},
		{"/a/x", "shallow"},
		{"/a/b", "medium"},
		{"/a/b/x", "medium"},
		{"/a/b/c", "deep"},
		{"/a/b/c/x", "deep"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			route := r.Match("example.com", tt.path)
			if route == nil {
				t.Fatal("Match() = nil")
			}
			if route.ServiceName != tt.wantService {
				t.Errorf("Match(%q).ServiceName = %q, want %q", tt.path, route.ServiceName, tt.wantService)
			}
		})
	}
}

func TestPathEdgeCases(t *testing.T) {
	routes := []compose.Route{
		{Host: "example.com", PathPrefix: "/api", ServiceName: "api", ServicePort: 80},
		{Host: "example.com", PathPrefix: "/", ServiceName: "root", ServicePort: 80},
	}
	r := New(routes)

	tests := []struct {
		name        string
		path        string
		wantService string
		wantNil     bool
	}{
		{
			name:        "exact match /api",
			path:        "/api",
			wantService: "api",
		},
		{
			name:        "with trailing slash /api/",
			path:        "/api/",
			wantService: "api",
		},
		{
			name:        "subpath /api/users",
			path:        "/api/users",
			wantService: "api",
		},
		{
			name:        "similar prefix /apiv2 should match root",
			path:        "/apiv2",
			wantService: "root", // /apiv2 doesn't start with /api/ or equal /api
		},
		{
			name:        "encoded path /api/%2F",
			path:        "/api/%2F",
			wantService: "api",
		},
		{
			name:        "path with query-like chars /api/users?foo",
			path:        "/api/users?foo", // Note: this is path, not query string
			wantService: "api",
		},
		{
			name:        "empty path",
			path:        "",
			wantService: "root",
		},
		{
			name:        "double slash /api//users",
			path:        "/api//users",
			wantService: "api",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := r.Match("example.com", tt.path)
			if tt.wantNil {
				if route != nil {
					t.Errorf("Match(%q) = %v, want nil", tt.path, route)
				}
				return
			}
			if route == nil {
				t.Fatalf("Match(%q) = nil, want route", tt.path)
			}
			if route.ServiceName != tt.wantService {
				t.Errorf("Match(%q).ServiceName = %q, want %q", tt.path, route.ServiceName, tt.wantService)
			}
		})
	}
}

func TestTrailingSlashInPrefix(t *testing.T) {
	// Test that prefix with trailing slash works correctly
	routes := []compose.Route{
		{Host: "example.com", PathPrefix: "/api/", ServiceName: "api-slash", ServicePort: 80},
		{Host: "example.com", PathPrefix: "/", ServiceName: "root", ServicePort: 80},
	}
	r := New(routes)

	tests := []struct {
		path        string
		wantService string
	}{
		{"/api/", "api-slash"},
		{"/api/users", "api-slash"},
		{"/api", "root"}, // /api doesn't match /api/ prefix
		{"/", "root"},
	}

	for _, tt := range tests {
		t.Run(tt.path, func(t *testing.T) {
			route := r.Match("example.com", tt.path)
			if route == nil {
				t.Fatalf("Match(%q) = nil", tt.path)
			}
			if route.ServiceName != tt.wantService {
				t.Errorf("Match(%q).ServiceName = %q, want %q", tt.path, route.ServiceName, tt.wantService)
			}
		})
	}
}

func TestCaseSensitivity(t *testing.T) {
	routes := []compose.Route{
		{Host: "Example.COM", PathPrefix: "/API", ServiceName: "api", ServicePort: 80},
	}
	r := New(routes)

	// Hosts are typically case-insensitive, but our implementation is case-sensitive
	// Paths are case-sensitive
	tests := []struct {
		name    string
		host    string
		path    string
		wantNil bool
	}{
		{"exact match", "Example.COM", "/API", false},
		{"lowercase host", "example.com", "/API", true},
		{"lowercase path", "Example.COM", "/api", true},
		{"all lowercase", "example.com", "/api", true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := r.Match(tt.host, tt.path)
			if tt.wantNil && route != nil {
				t.Errorf("Match(%q, %q) should be nil", tt.host, tt.path)
			}
			if !tt.wantNil && route == nil {
				t.Errorf("Match(%q, %q) should not be nil", tt.host, tt.path)
			}
		})
	}
}

func TestWildcardHostMatch(t *testing.T) {
	routes := []compose.Route{
		{Host: "tenant.com", PathPrefix: "/", ServiceName: "marketing", ServicePort: 80,
			RedirectFrom: []string{"www.tenant.com"}},
		{Host: "*.tenant.com", PathPrefix: "/", ServiceName: "tenant-app", ServicePort: 8080},
	}
	r := New(routes)

	tests := []struct {
		name        string
		host        string
		path        string
		wantService string
		wantNil     bool
	}{
		{
			name:        "exact host match - apex domain",
			host:        "tenant.com",
			path:        "/",
			wantService: "marketing",
		},
		{
			name:        "wildcard match - acme subdomain",
			host:        "acme.tenant.com",
			path:        "/",
			wantService: "tenant-app",
		},
		{
			name:        "wildcard match - another subdomain",
			host:        "bigcorp.tenant.com",
			path:        "/",
			wantService: "tenant-app",
		},
		{
			name:        "wildcard match with path",
			host:        "acme.tenant.com",
			path:        "/dashboard",
			wantService: "tenant-app",
		},
		{
			name:    "no match - different domain",
			host:    "other.com",
			path:    "/",
			wantNil: true,
		},
		{
			name:    "no match - deeper subdomain",
			host:    "sub.acme.tenant.com",
			path:    "/",
			wantNil: true, // *.tenant.com doesn't match sub.acme.tenant.com
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			route := r.Match(tt.host, tt.path)
			if tt.wantNil {
				if route != nil {
					t.Errorf("Match(%q, %q) = %v, want nil", tt.host, tt.path, route.ServiceName)
				}
				return
			}
			if route == nil {
				t.Fatalf("Match(%q, %q) = nil, want route", tt.host, tt.path)
			}
			if route.ServiceName != tt.wantService {
				t.Errorf("Match(%q, %q).ServiceName = %q, want %q", tt.host, tt.path, route.ServiceName, tt.wantService)
			}
		})
	}
}

func TestWildcardRedirectPriority(t *testing.T) {
	// www.tenant.com should redirect, not match wildcard
	routes := []compose.Route{
		{Host: "tenant.com", PathPrefix: "/", ServiceName: "marketing", ServicePort: 80,
			RedirectFrom: []string{"www.tenant.com"}},
		{Host: "*.tenant.com", PathPrefix: "/", ServiceName: "tenant-app", ServicePort: 8080},
	}
	r := New(routes)

	// www.tenant.com should redirect to tenant.com
	redirect := r.Redirect("www.tenant.com")
	if redirect == nil {
		t.Fatal("Redirect(www.tenant.com) = nil, want redirect to tenant.com")
	}
	if redirect.Host != "tenant.com" {
		t.Errorf("Redirect(www.tenant.com).Host = %q, want %q", redirect.Host, "tenant.com")
	}

	// Other subdomains should not redirect
	if r.Redirect("acme.tenant.com") != nil {
		t.Error("Redirect(acme.tenant.com) should be nil")
	}
}

func TestWildcardWithPathPrefixes(t *testing.T) {
	routes := []compose.Route{
		{Host: "*.tenant.com", PathPrefix: "/api", ServiceName: "api", ServicePort: 8080},
		{Host: "*.tenant.com", PathPrefix: "/", ServiceName: "app", ServicePort: 3000},
	}
	r := New(routes)

	tests := []struct {
		host        string
		path        string
		wantService string
	}{
		{"acme.tenant.com", "/", "app"},
		{"acme.tenant.com", "/dashboard", "app"},
		{"acme.tenant.com", "/api", "api"},
		{"acme.tenant.com", "/api/users", "api"},
		{"bigcorp.tenant.com", "/api/v2", "api"},
	}

	for _, tt := range tests {
		t.Run(tt.host+tt.path, func(t *testing.T) {
			route := r.Match(tt.host, tt.path)
			if route == nil {
				t.Fatalf("Match(%q, %q) = nil", tt.host, tt.path)
			}
			if route.ServiceName != tt.wantService {
				t.Errorf("Match(%q, %q).ServiceName = %q, want %q", tt.host, tt.path, route.ServiceName, tt.wantService)
			}
		})
	}
}

func TestHostsIncludesWildcards(t *testing.T) {
	routes := []compose.Route{
		{Host: "tenant.com", PathPrefix: "/", ServiceName: "marketing", ServicePort: 80,
			RedirectFrom: []string{"www.tenant.com"}},
		{Host: "*.tenant.com", PathPrefix: "/", ServiceName: "tenant-app", ServicePort: 8080},
	}
	r := New(routes)

	hosts := r.Hosts()
	expected := map[string]bool{
		"tenant.com":     true,
		"www.tenant.com": true,
		"*.tenant.com":   true,
	}

	if len(hosts) != len(expected) {
		t.Errorf("Hosts() returned %d hosts, want %d: %v", len(hosts), len(expected), hosts)
	}

	for _, h := range hosts {
		if !expected[h] {
			t.Errorf("Hosts() contains unexpected host %q", h)
		}
	}
}
