# liteproxy

A lightweight reverse proxy that reads Docker Compose files and routes traffic based on labels. No Docker socket required — just parse the compose file and proxy.

## Features

- **Zero config files** — all routing defined via compose labels
- **Longest-prefix matching** — multiple services can share a host with different paths
- **Automatic HTTPS** — Let's Encrypt certificates via autocert
- **Hot reload** — SIGHUP or file watching to reload routes without restart
- **Single binary** — easy to deploy

## Quick Start

**Important:** Liteproxy must run inside Docker Compose to resolve service names. Service names like `marketing` or `api` only resolve within Docker's network.

```bash
# Try the example
docker compose -f example-compose.yaml up --build

# Test routing
curl -H "Host: example.com" http://localhost:8080/        # → nginx
curl -H "Host: example.com" http://localhost:8080/api     # → api service
curl -I -H "Host: www.example.com" http://localhost:8080/ # → 301 redirect
```

For local development without Docker:
```bash
go build -o liteproxy .
LITEPROXY_HTTPS_ENABLED=false ./liteproxy
# Note: upstream services won't resolve unless you add them to /etc/hosts
```

## How It Works

1. Liteproxy reads your `compose.yaml` file
2. Services with `liteproxy.*` labels become routing rules
3. Incoming requests are matched by host + longest path prefix
4. Requests are reverse-proxied to the matching service

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

## Label Schema

Add these labels to any service you want to proxy:

| Label | Required | Default | Description |
|-------|----------|---------|-------------|
| `liteproxy.host` | yes | — | Domain to match |
| `liteproxy.port` | yes | — | Container port to proxy to |
| `liteproxy.path` | no | `/` | Path prefix (longest match wins) |
| `liteproxy.strip_prefix` | no | `true` | Strip path prefix before forwarding |
| `liteproxy.redirect_from` | no | — | Comma-separated domains to 301 redirect |
| `liteproxy.passhost` | no | `false` | Pass original Host header to upstream |

## Example Compose File

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
      LITEPROXY_WATCH: "true"

  marketing:
    image: nginx:alpine
    labels:
      liteproxy.host: "example.com"
      liteproxy.port: "80"
      liteproxy.redirect_from: "www.example.com,old.example.com"

  api:
    image: myapi:latest
    labels:
      liteproxy.host: "example.com"
      liteproxy.port: "8080"
      liteproxy.path: "/api"

  redis:
    image: redis:alpine
    # no labels = not proxied
```

With this configuration:
- `example.com/` → marketing:80
- `example.com/api/*` → api:8080
- `www.example.com/*` → 301 redirect to `example.com`

## Routing Rules

**Longest prefix wins:** When multiple services share a host, the most specific path matches first.

```
example.com/api/users  → matches /api  → api service
example.com/about      → matches /     → marketing service
```

**Path stripping:** By default, the path prefix is stripped before forwarding to upstream.

```
Request: example.com/api/users
Route:   /api → api:8080
Upstream receives: /users
```

To preserve the full path, set `liteproxy.strip_prefix: "false"`:

```yaml
labels:
  liteproxy.host: "example.com"
  liteproxy.port: "8080"
  liteproxy.path: "/api"
  liteproxy.strip_prefix: "false"  # upstream receives /api/users
```

**Redirects:** Requests to `redirect_from` domains return 301 to the primary host, preserving the path and query string.

```
www.example.com/pricing?plan=pro → 301 → example.com/pricing?plan=pro
```

## Configuration

Liteproxy is configured via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `LITEPROXY_COMPOSE_FILE` | `./compose.yaml` | Path to compose file |
| `LITEPROXY_HTTP_PORT` | `80` | HTTP listen port |
| `LITEPROXY_HTTPS_PORT` | `443` | HTTPS listen port |
| `LITEPROXY_ACME_EMAIL` | (required for HTTPS) | Let's Encrypt email |
| `LITEPROXY_ACME_DIR` | `./certs` | Certificate storage directory |
| `LITEPROXY_HTTPS_ENABLED` | `true` | Enable HTTPS |
| `LITEPROXY_WATCH` | `false` | Auto-reload on compose file changes |

## Reloading Configuration

**SIGHUP (always available):**
```bash
kill -HUP $(pidof liteproxy)
```

**File watching (opt-in):**
```bash
LITEPROXY_WATCH=true liteproxy
```

When enabled, liteproxy automatically reloads when compose.yaml changes (debounced to 500ms).

## Building

```bash
# Standard build
go build -o liteproxy .

# Smaller binary (strips debug info)
go build -ldflags="-s -w" -o liteproxy .
```

## License

MIT License - see [LICENSE](LICENSE) file.
