# liteproxy

A lightweight reverse proxy that reads Docker Compose files and routes traffic based on labels. No Docker socket required — just parse the compose file and proxy.

## Features

- **Zero config files** — all routing defined via compose labels
- **Longest-prefix matching** — multiple services can share a host with different paths
- **Wildcard subdomains** — `*.tenant.com` for multi-tenant SaaS routing
- **TCP passthrough** — forward raw TCP for services that handle their own TLS
- **Automatic HTTPS** — Let's Encrypt certificates via autocert (optional)
- **Load balancer friendly** — HTTP-only mode for running behind LB/CDN
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
| `liteproxy.host` | yes | — | Domain to match (supports `*.example.com` wildcards) |
| `liteproxy.port` | yes | — | Container port to proxy to |
| `liteproxy.path` | no | `/` | Path prefix (longest match wins) |
| `liteproxy.strip_prefix` | no | `false` | Strip path prefix before forwarding |
| `liteproxy.redirect_from` | no | — | Comma-separated domains to 301 redirect |
| `liteproxy.passhost` | no | `false` | Pass original Host header to upstream |
| `liteproxy.passthrough` | no | `false` | Forward raw TCP without TLS termination |

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
      LITEPROXY_HTTPS_ENABLED: "true"
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

**Path preservation:** By default, the full path is preserved when forwarding to upstream.

```
Request: example.com/api/users
Route:   /api → api:8080
Upstream receives: /api/users
```

To strip the path prefix, set `liteproxy.strip_prefix: "true"`:

```yaml
labels:
  liteproxy.host: "example.com"
  liteproxy.port: "8080"
  liteproxy.path: "/api"
  liteproxy.strip_prefix: "true"  # upstream receives /users
```

**Redirects:** Requests to `redirect_from` domains return 301 to the primary host, preserving the path and query string.

```
www.example.com/pricing?plan=pro → 301 → example.com/pricing?plan=pro
```

## Wildcard Subdomain Routing

For multi-tenant SaaS apps, use wildcard hosts (`*.tenant.com`):

```yaml
services:
  marketing:
    image: marketing:latest
    labels:
      liteproxy.host: "tenant.com"
      liteproxy.port: "80"
      liteproxy.redirect_from: "www.tenant.com"

  tenant-app:
    image: tenant-app:latest
    labels:
      liteproxy.host: "*.tenant.com"
      liteproxy.port: "8080"
```

**Routing priority:**
1. Redirects are checked first (`www.tenant.com` → 301 to `tenant.com`)
2. Exact host matches (`tenant.com` → marketing)
3. Wildcard matches (`acme.tenant.com` → tenant-app)

**Note:** Wildcards match one subdomain level only:
- ✅ `acme.tenant.com` matches `*.tenant.com`
- ❌ `sub.acme.tenant.com` does NOT match `*.tenant.com`

## TCP Passthrough

For services that need to handle their own TLS (mail servers, custom protocols), use passthrough mode:

```yaml
services:
  liteproxy:
    image: liteproxy:latest
    ports:
      - "80:80"
      - "443:443"

  # Normal reverse proxy - liteproxy terminates TLS
  webapp:
    image: webapp:latest
    labels:
      liteproxy.host: "app.example.com"
      liteproxy.port: "8080"

  # Passthrough - mail server handles its own TLS
  mailserver:
    image: mailserver:latest
    labels:
      liteproxy.host: "mail.example.com"
      liteproxy.port: "443"
      liteproxy.passthrough: "true"
```

**How passthrough works:**
1. Liteproxy peeks at the TLS ClientHello (SNI) or HTTP Host header
2. If the host matches a passthrough route, raw TCP is forwarded to the backend
3. The backend handles TLS termination and all protocol details

**Use cases:**
- Mail servers (Postfix, Dovecot) that need their own certificates
- Services with mutual TLS (mTLS) requirements
- Custom protocols over TLS
- Services that must see the original client certificate

**Performance:** Passthrough adds ~10-30 microseconds latency. Data transfer is network-bound, not CPU-bound.

## Configuration

Liteproxy is configured via environment variables:

| Variable | Default | Description |
|----------|---------|-------------|
| `LITEPROXY_COMPOSE_FILE` | `./compose.yaml` | Path to compose file |
| `LITEPROXY_HTTP_PORT` | `80` | HTTP listen port |
| `LITEPROXY_HTTPS_PORT` | `443` | HTTPS listen port |
| `LITEPROXY_HTTPS_ENABLED` | `false` | Enable HTTPS with autocert |
| `LITEPROXY_ACME_EMAIL` | — | Let's Encrypt email (required if HTTPS enabled) |
| `LITEPROXY_ACME_DIR` | `./certs` | Certificate storage directory |
| `LITEPROXY_WATCH` | `false` | Auto-reload on compose file changes |

## Running Behind a Load Balancer

When running behind a load balancer that handles TLS termination, use HTTP-only mode (the default):

```yaml
services:
  liteproxy:
    image: liteproxy:latest
    ports:
      - "80:80"
    volumes:
      - ./compose.yaml:/etc/liteproxy/compose.yaml:ro
    environment:
      LITEPROXY_COMPOSE_FILE: /etc/liteproxy/compose.yaml
      # LITEPROXY_HTTPS_ENABLED defaults to false
```

For direct internet exposure with automatic certificates:

```yaml
environment:
  LITEPROXY_HTTPS_ENABLED: "true"
  LITEPROXY_ACME_EMAIL: you@example.com
```

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
