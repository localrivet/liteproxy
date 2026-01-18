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
