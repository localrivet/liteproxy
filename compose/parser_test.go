package compose

import (
	"testing"
)

func TestParse(t *testing.T) {
	tests := []struct {
		name      string
		yaml      string
		wantCount int
		wantErr   bool
	}{
		{
			name: "basic service with required labels",
			yaml: `
services:
  web:
    image: nginx
    labels:
      liteproxy.host: "example.com"
      liteproxy.port: "80"
`,
			wantCount: 1,
			wantErr:   false,
		},
		{
			name: "multiple services",
			yaml: `
services:
  web:
    image: nginx
    labels:
      liteproxy.host: "example.com"
      liteproxy.port: "80"
  api:
    image: api
    labels:
      liteproxy.host: "example.com"
      liteproxy.port: "8080"
      liteproxy.path: "/api"
`,
			wantCount: 2,
			wantErr:   false,
		},
		{
			name: "service without labels is ignored",
			yaml: `
services:
  web:
    image: nginx
    labels:
      liteproxy.host: "example.com"
      liteproxy.port: "80"
  redis:
    image: redis
`,
			wantCount: 1,
			wantErr:   false,
		},
		{
			name: "missing host label",
			yaml: `
services:
  web:
    image: nginx
    labels:
      liteproxy.port: "80"
`,
			wantCount: 0,
			wantErr:   true,
		},
		{
			name: "missing port label",
			yaml: `
services:
  web:
    image: nginx
    labels:
      liteproxy.host: "example.com"
`,
			wantCount: 0,
			wantErr:   true,
		},
		{
			name: "invalid port",
			yaml: `
services:
  web:
    image: nginx
    labels:
      liteproxy.host: "example.com"
      liteproxy.port: "not-a-number"
`,
			wantCount: 0,
			wantErr:   true,
		},
		{
			name: "empty file",
			yaml: `
services: {}
`,
			wantCount: 0,
			wantErr:   false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			routes, err := Parse([]byte(tt.yaml), "test.yaml")
			if (err != nil) != tt.wantErr {
				t.Errorf("Parse() error = %v, wantErr %v", err, tt.wantErr)
				return
			}
			if !tt.wantErr && len(routes) != tt.wantCount {
				t.Errorf("Parse() got %d routes, want %d", len(routes), tt.wantCount)
			}
		})
	}
}

func TestParseRouteFields(t *testing.T) {
	yaml := `
services:
  web:
    image: nginx
    labels:
      liteproxy.host: "example.com"
      liteproxy.port: "8080"
      liteproxy.path: "/api"
      liteproxy.passhost: "true"
      liteproxy.strip_prefix: "false"
      liteproxy.redirect_from: "www.example.com, old.example.com"
`
	routes, err := Parse([]byte(yaml), "test.yaml")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	r := routes[0]

	if r.Host != "example.com" {
		t.Errorf("Host = %q, want %q", r.Host, "example.com")
	}
	if r.ServicePort != 8080 {
		t.Errorf("ServicePort = %d, want %d", r.ServicePort, 8080)
	}
	if r.PathPrefix != "/api" {
		t.Errorf("PathPrefix = %q, want %q", r.PathPrefix, "/api")
	}
	if !r.PassHostHeader {
		t.Error("PassHostHeader = false, want true")
	}
	if r.StripPrefix {
		t.Error("StripPrefix = true, want false")
	}
	if len(r.RedirectFrom) != 2 {
		t.Fatalf("RedirectFrom has %d items, want 2", len(r.RedirectFrom))
	}
	if r.RedirectFrom[0] != "www.example.com" {
		t.Errorf("RedirectFrom[0] = %q, want %q", r.RedirectFrom[0], "www.example.com")
	}
	if r.RedirectFrom[1] != "old.example.com" {
		t.Errorf("RedirectFrom[1] = %q, want %q", r.RedirectFrom[1], "old.example.com")
	}
}

func TestParseDefaults(t *testing.T) {
	yaml := `
services:
  web:
    image: nginx
    labels:
      liteproxy.host: "example.com"
      liteproxy.port: "80"
`
	routes, err := Parse([]byte(yaml), "test.yaml")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	r := routes[0]

	// Check defaults
	if r.PathPrefix != "/" {
		t.Errorf("PathPrefix default = %q, want %q", r.PathPrefix, "/")
	}
	if r.PassHostHeader {
		t.Error("PassHostHeader default = true, want false")
	}
	if !r.StripPrefix {
		t.Error("StripPrefix default = false, want true")
	}
	if len(r.RedirectFrom) != 0 {
		t.Errorf("RedirectFrom default has %d items, want 0", len(r.RedirectFrom))
	}
}

func TestParseServiceName(t *testing.T) {
	yaml := `
services:
  my-awesome-service:
    image: nginx
    labels:
      liteproxy.host: "example.com"
      liteproxy.port: "80"
`
	routes, err := Parse([]byte(yaml), "test.yaml")
	if err != nil {
		t.Fatalf("Parse() error = %v", err)
	}
	if len(routes) != 1 {
		t.Fatalf("expected 1 route, got %d", len(routes))
	}

	if routes[0].ServiceName != "my-awesome-service" {
		t.Errorf("ServiceName = %q, want %q", routes[0].ServiceName, "my-awesome-service")
	}
}
