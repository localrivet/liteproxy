# Liteproxy MVP - Lightweight Compose-Based Reverse Proxy

## Overview

Liteproxy is a minimal reverse proxy that reads Docker Compose files and routes traffic to services based on labels. No Docker socket required — just parse the compose file and proxy.

## Tech Stack

- **Language:** Go 1.20+ (required for `httputil.ReverseProxy.Rewrite`)
- **Compose Parsing:** `github.com/compose-spec/compose-go/v2` (official Docker Compose Go library)
- **HTTP Router:** Standard library `net/http` + `httputil.ReverseProxy`
- **HTTPS:** `golang.org/x/crypto/acme/autocert` for automatic Let's Encrypt
- **File Watching:** `github.com/fsnotify/fsnotify` (optional, for auto-reload)

## MVP Scope

### What It Does

1. Reads a `compose.yaml` file
2. Finds services with `liteproxy.*` labels
3. Builds a routing table: domain/path → service:port
4. Serves HTTP/HTTPS and reverse proxies requests

### What It Doesn't Do (MVP)

- Docker socket integration (we parse the file, not watch containers)
- Health checks
- Load balancing
- Middleware

## Label Schema

Any service with `liteproxy.*` labels is proxied. No labels = ignored.

| Label | Required | Default | Description |
|-------|----------|---------|-------------|
| `liteproxy.host` | yes | — | Domain to match |
| `liteproxy.port` | yes | — | Container port |
| `liteproxy.path` | no | `/` | Path prefix (longest match wins) |
| `liteproxy.redirect_from` | no | — | Comma-separated domains to 301 redirect |
| `liteproxy.passhost` | no | `false` | Pass original Host header to upstream |

```yaml
services:
  marketing:
    image: marketing:latest
    labels:
      liteproxy.host: "abc.com"
      liteproxy.port: "3000"
      # path defaults to "/"
      liteproxy.redirect_from: "www.abc.com,old.abc.com"

  api:
    image: api:latest
    labels:
      liteproxy.host: "abc.com"
      liteproxy.port: "8080"
      liteproxy.path: "/api"

  redis:
    image: redis:alpine
    # no labels = not proxied
```

### Routing Rules

**Longest prefix wins:** When multiple services share a host, the most specific path matches.

- `abc.com/api/users` → matches `/api` → api service
- `abc.com/about` → matches `/` → marketing service

**Redirects:** Requests to `redirect_from` domains return 301 to the primary host, preserving the path.

- `www.abc.com/pricing` → 301 → `abc.com/pricing`

## Configuration

Liteproxy itself is configured via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `LITEPROXY_COMPOSE_FILE` | `./compose.yaml` | Path to compose file |
| `LITEPROXY_HTTP_PORT` | `80` | HTTP listen port |
| `LITEPROXY_HTTPS_PORT` | `443` | HTTPS listen port |
| `LITEPROXY_ACME_EMAIL` | (required for HTTPS) | Let's Encrypt email |
| `LITEPROXY_ACME_DIR` | `./certs` | Certificate storage |
| `LITEPROXY_HTTPS_ENABLED` | `true` | Enable HTTPS |
| `LITEPROXY_WATCH` | `false` | Auto-reload on compose file changes |

### Reloading Configuration

**SIGHUP (always available):**
```bash
kill -HUP $(pidof liteproxy)
```

**File watching (opt-in):**
```bash
LITEPROXY_WATCH=true liteproxy
```
Automatically reloads when compose.yaml changes. Uses `fsnotify`.

## Architecture

```
┌─────────────────────────────────────────────────────────┐
│                      Liteproxy                          │
│                                                         │
│  ┌──────────────┐    ┌──────────────┐    ┌──────────┐  │
│  │   Compose    │───▶│   Router     │───▶│  Proxy   │  │
│  │   Parser     │    │   Table      │    │  Handler │  │
│  └──────────────┘    └──────────────┘    └──────────┘  │
│         │                                      │        │
│         ▼                                      ▼        │
│  ┌──────────────┐                     ┌──────────────┐ │
│  │ compose.yaml │                     │  Upstream    │ │
│  └──────────────┘                     │  Services    │ │
└─────────────────────────────────────────────────────────┘
```

## Request Flow

**Normal request:**
1. Request arrives at `example.com/api/users`
2. Router finds all routes for host `example.com`
3. Longest prefix match: `/api` beats `/`
4. Reverse proxy forwards to `http://api:8080/api/users`
5. Response returned to client

**Redirect request:**
1. Request arrives at `www.example.com/pricing`
2. Router finds `www.example.com` in redirect_from
3. Returns 301 to `https://example.com/pricing`

## Code Structure

```
liteproxy/
├── main.go           # Entry point, config loading, signal handling
├── compose/
│   └── parser.go     # Parse compose.yaml, extract routes
├── router/
│   └── router.go     # Build routing table, longest-prefix match
├── proxy/
│   └── handler.go    # Reverse proxy + redirect handler
├── tls/
│   └── autocert.go   # Let's Encrypt integration
└── watcher/
    └── watcher.go    # Optional fsnotify file watching
```

## Key Types

```go
// Route represents a single routing rule
type Route struct {
    Host           string
    PathPrefix     string
    ServiceName    string
    ServicePort    int
    PassHostHeader bool      // pass original Host header to upstream
    RedirectFrom   []string  // domains that 301 to this route
}

// Router holds the routing table
type Router struct {
    routes    []Route
    redirects map[string]*Route  // redirect domain → target route
}

// Match finds the route for a request (longest prefix wins)
func (r *Router) Match(host, path string) *Route

// Redirect checks if host should redirect, returns target route
func (r *Router) Redirect(host string) *Route
```

## Reverse Proxy Implementation (Traefik-Style)

Traefik uses Go's standard library `httputil.ReverseProxy` with the modern `Rewrite` function (Go 1.20+). This is the cleanest approach — we'll follow the same pattern.

### Why Rewrite over Director

Go 1.20 introduced `Rewrite` to replace the older `Director` function:

- **Director** (legacy): Modifies the request in-place, causing issues with hop-by-hop headers
- **Rewrite** (modern): Separates inbound (`In`) and outbound (`Out`) requests cleanly

### Core Implementation

```go
func buildProxy(target *url.URL, passHostHeader bool) http.Handler {
    return &httputil.ReverseProxy{
        Rewrite: func(pr *httputil.ProxyRequest) {
            // Route to target (joins paths: /base + /dir = /base/dir)
            pr.SetURL(target)
            
            // Preserve original host header if configured
            if passHostHeader {
                pr.Out.Host = pr.In.Host
            }
            
            // Set standard proxy headers
            pr.SetXForwarded()
        },
        
        // Flush every 100ms for streaming responses
        FlushInterval: 100 * time.Millisecond,
        
        // Custom error handling
        ErrorHandler: func(w http.ResponseWriter, r *http.Request, err error) {
            log.Printf("proxy error: %v", err)
            w.WriteHeader(http.StatusBadGateway)
        },
    }
}
```

### What SetURL Does

```go
pr.SetURL(target)
```

- Rewrites `pr.Out.URL` to target's scheme, host, and base path
- Joins paths: if target is `http://api:8080/v1` and request is `/users`, result is `/v1/users`
- Rewrites `Host` header to match target (unless overridden)

### What SetXForwarded Does

```go
pr.SetXForwarded()
```

Sets three headers on the outbound request:

| Header | Value |
|--------|-------|
| `X-Forwarded-For` | Client IP (appends if existing) |
| `X-Forwarded-Host` | Original host requested by client |
| `X-Forwarded-Proto` | `http` or `https` based on inbound TLS |

### Hop-by-Hop Header Handling

`httputil.ReverseProxy` automatically strips these headers (per RFC 9110):

- `Connection`
- `Proxy-Connection`
- `Keep-Alive`
- `Proxy-Authenticate`
- `Proxy-Authorization`
- `TE`
- `Trailer`
- `Transfer-Encoding`
- `Upgrade`

### Optional: Response Modification

```go
ModifyResponse: func(resp *http.Response) error {
    // Add custom headers, log responses, etc.
    resp.Header.Set("X-Proxy", "liteproxy")
    return nil
},
```

### Optional: Buffer Pool (Performance)

For high-throughput scenarios, reuse buffers:

```go
type bufferPool struct {
    pool sync.Pool
}

func (b *bufferPool) Get() []byte {
    if v := b.pool.Get(); v != nil {
        return v.([]byte)
    }
    return make([]byte, 32*1024)
}

func (b *bufferPool) Put(buf []byte) {
    b.pool.Put(buf)
}
```

Then set `BufferPool: &bufferPool{}` on the ReverseProxy.

## MVP Implementation Steps

1. **Compose Parser** - Use compose-go to read file, extract labeled services
2. **Router** - Build routing table with longest-prefix matching + redirect map
3. **Proxy Handler** - Reverse proxy + 301 redirect handling
4. **HTTP Server** - Serve on port 80, route requests
5. **HTTPS/TLS** - Add autocert for automatic Let's Encrypt
6. **Reload** - SIGHUP handler + optional fsnotify file watcher

## Usage

```bash
# Run liteproxy pointing at your compose file
LITEPROXY_COMPOSE_FILE=./compose.yaml \
LITEPROXY_ACME_EMAIL=you@example.com \
liteproxy
```

Or in the compose file itself:

```yaml
services:
  liteproxy:
    image: liteproxy:latest
    ports:
      - "80:80"
      - "443:443"
    volumes:
      - ./compose.yaml:/etc/liteproxy/compose.yaml:ro
      - ./certs:/certs
    environment:
      LITEPROXY_COMPOSE_FILE: /etc/liteproxy/compose.yaml
      LITEPROXY_ACME_EMAIL: you@example.com
      LITEPROXY_ACME_DIR: /certs
      LITEPROXY_WATCH: "true"

  marketing:
    image: marketing:latest
    labels:
      liteproxy.host: "example.com"
      liteproxy.port: "3000"
      liteproxy.redirect_from: "www.example.com"

  api:
    image: api:latest
    labels:
      liteproxy.host: "example.com"
      liteproxy.port: "8080"
      liteproxy.path: "/api"
```

## Success Criteria

- [ ] Parses compose.yaml using compose-go
- [ ] Routes requests by host + longest path prefix
- [ ] 301 redirects for redirect_from domains
- [ ] Reverse proxies to correct upstream
- [ ] Automatic HTTPS via Let's Encrypt
- [ ] SIGHUP reload support
- [ ] Optional file watching with fsnotify
- [ ] Single binary, zero config files
- [ ] < 10MB binary size
- [ ] < 50ms added latency
