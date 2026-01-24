package main

import (
	"crypto/tls"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"

	"github.com/localrivet/liteproxy/compose"
	"github.com/localrivet/liteproxy/passthrough"
	"github.com/localrivet/liteproxy/proxy"
	"github.com/localrivet/liteproxy/router"
	liteTLS "github.com/localrivet/liteproxy/tls"
	"github.com/localrivet/liteproxy/watcher"
	"golang.org/x/crypto/acme/autocert"
)

// Config holds all configuration loaded from environment variables
type Config struct {
	ComposeFile  string
	HTTPPort     int
	HTTPSPort    int
	ACMEEmail    string
	ACMEDir      string
	HTTPSEnabled bool
	Watch        bool
}

func loadConfig() Config {
	cfg := Config{
		ComposeFile:  getEnv("LITEPROXY_COMPOSE_FILE", "./compose.yaml"),
		HTTPPort:     getEnvInt("LITEPROXY_HTTP_PORT", 80),
		HTTPSPort:    getEnvInt("LITEPROXY_HTTPS_PORT", 443),
		ACMEEmail:    os.Getenv("LITEPROXY_ACME_EMAIL"),
		ACMEDir:      getEnv("LITEPROXY_ACME_DIR", "./certs"),
		HTTPSEnabled: getEnvBool("LITEPROXY_HTTPS_ENABLED", false),
		Watch:        getEnvBool("LITEPROXY_WATCH", false),
	}

	if cfg.HTTPSEnabled && cfg.ACMEEmail == "" {
		log.Fatal("LITEPROXY_ACME_EMAIL is required when HTTPS is enabled")
	}

	return cfg
}

func getEnv(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func getEnvInt(key string, fallback int) int {
	if v := os.Getenv(key); v != "" {
		if i, err := strconv.Atoi(v); err == nil {
			return i
		}
	}
	return fallback
}

func getEnvBool(key string, fallback bool) bool {
	if v := os.Getenv(key); v != "" {
		return v == "true" || v == "1" || v == "yes"
	}
	return fallback
}

func main() {
	cfg := loadConfig()

	log.Printf("liteproxy starting")
	log.Printf("  compose file: %s", cfg.ComposeFile)
	log.Printf("  HTTP port: %d", cfg.HTTPPort)
	log.Printf("  HTTPS enabled: %v", cfg.HTTPSEnabled)
	if cfg.HTTPSEnabled {
		log.Printf("  HTTPS port: %d", cfg.HTTPSPort)
	}
	log.Printf("  watch mode: %v", cfg.Watch)

	// Parse compose file
	routes, err := compose.ParseFile(cfg.ComposeFile)
	if err != nil {
		log.Fatalf("failed to parse compose file: %v", err)
	}
	log.Printf("loaded %d routes", len(routes))
	for _, r := range routes {
		extra := ""
		if r.Passthrough {
			extra = " [passthrough]"
		}
		log.Printf("  %s%s -> %s:%d%s", r.Host, r.PathPrefix, r.ServiceName, r.ServicePort, extra)
		if len(r.RedirectFrom) > 0 {
			log.Printf("    redirects from: %v", r.RedirectFrom)
		}
	}

	// Create router
	rtr := router.New(routes)

	// Determine scheme for redirects
	scheme := "http"
	if cfg.HTTPSEnabled {
		scheme = "https"
	}

	// Create proxy handler
	handler := proxy.New(rtr, scheme)

	// Check if we have passthrough routes
	hasPassthrough := rtr.HasPassthroughRoutes()
	if hasPassthrough {
		log.Println("passthrough routes detected - using TCP routing")
	}

	// State for hot reload
	var (
		mu             sync.Mutex
		certManager    *autocert.Manager
		httpListener   *passthrough.Listener
		httpsListener  *passthrough.Listener
	)

	// Reload function
	reload := func() {
		mu.Lock()
		defer mu.Unlock()

		log.Println("reloading configuration...")

		newRoutes, err := compose.ParseFile(cfg.ComposeFile)
		if err != nil {
			log.Printf("reload failed: %v", err)
			return
		}

		newRouter := router.New(newRoutes)
		handler.UpdateRouter(newRouter)

		// Update passthrough listeners
		if httpListener != nil {
			httpListener.UpdateRouter(newRouter)
		}
		if httpsListener != nil {
			httpsListener.UpdateRouter(newRouter)
		}

		log.Printf("reloaded %d routes", len(newRoutes))
		for _, r := range newRoutes {
			extra := ""
			if r.Passthrough {
				extra = " [passthrough]"
			}
			log.Printf("  %s%s -> %s:%d%s", r.Host, r.PathPrefix, r.ServiceName, r.ServicePort, extra)
		}

		// Update TLS hosts if HTTPS is enabled
		if cfg.HTTPSEnabled && certManager != nil {
			hosts := newRouter.Hosts()
			certManager = liteTLS.UpdateHosts(certManager, hosts)
		}
	}

	// Set up file watcher if enabled
	if cfg.Watch {
		stop, err := watcher.Watch(cfg.ComposeFile, reload)
		if err != nil {
			log.Printf("warning: failed to set up file watcher: %v", err)
		} else {
			defer stop()
			log.Println("file watching enabled")
		}
	}

	// Set up signal handling for SIGHUP reload and graceful shutdown
	sigChan := make(chan os.Signal, 1)
	signal.Notify(sigChan, syscall.SIGHUP, syscall.SIGINT, syscall.SIGTERM)

	go func() {
		for sig := range sigChan {
			switch sig {
			case syscall.SIGHUP:
				reload()
			case syscall.SIGINT, syscall.SIGTERM:
				log.Println("shutting down...")
				os.Exit(0)
			}
		}
	}()

	// Start servers
	if cfg.HTTPSEnabled {
		hosts := rtr.Hosts()
		certManager = liteTLS.Manager(liteTLS.Config{
			Email:    cfg.ACMEEmail,
			CacheDir: cfg.ACMEDir,
			Hosts:    hosts,
		})
		tlsConfig := liteTLS.TLSConfig(certManager)

		// HTTP handler for ACME challenges + redirect
		httpHandler := certManager.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			target := "https://" + r.Host + r.URL.RequestURI()
			http.Redirect(w, r, target, http.StatusMovedPermanently)
		}))

		// HTTPS handler with TLS termination
		httpsHandler := &tlsHandler{handler: handler, tlsConfig: tlsConfig}

		if hasPassthrough {
			// Use passthrough listeners for both ports
			httpLn, err := net.Listen("tcp", ":"+strconv.Itoa(cfg.HTTPPort))
			if err != nil {
				log.Fatalf("failed to listen on HTTP port: %v", err)
			}
			httpsLn, err := net.Listen("tcp", ":"+strconv.Itoa(cfg.HTTPSPort))
			if err != nil {
				log.Fatalf("failed to listen on HTTPS port: %v", err)
			}

			httpListener = passthrough.NewHTTPListener(httpLn, rtr, httpHandler)
			httpsListener = passthrough.NewTLSListener(httpsLn, rtr, httpsHandler, tlsConfig)

			go func() {
				log.Printf("starting HTTP passthrough on :%d", cfg.HTTPPort)
				if err := httpListener.Serve(); err != nil {
					log.Fatalf("HTTP listener error: %v", err)
				}
			}()

			log.Printf("starting HTTPS passthrough on :%d", cfg.HTTPSPort)
			if err := httpsListener.Serve(); err != nil {
				log.Fatalf("HTTPS listener error: %v", err)
			}
		} else {
			// Standard HTTP servers (no passthrough routes)
			httpServer := &http.Server{
				Addr:    ":" + strconv.Itoa(cfg.HTTPPort),
				Handler: httpHandler,
			}
			httpsServer := &http.Server{
				Addr:      ":" + strconv.Itoa(cfg.HTTPSPort),
				Handler:   handler,
				TLSConfig: tlsConfig,
			}

			go func() {
				log.Printf("starting HTTP server on :%d (ACME + redirect)", cfg.HTTPPort)
				if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
					log.Fatalf("HTTP server error: %v", err)
				}
			}()

			log.Printf("starting HTTPS server on :%d", cfg.HTTPSPort)
			if err := httpsServer.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
				log.Fatalf("HTTPS server error: %v", err)
			}
		}
	} else {
		// HTTP only mode
		if hasPassthrough {
			httpLn, err := net.Listen("tcp", ":"+strconv.Itoa(cfg.HTTPPort))
			if err != nil {
				log.Fatalf("failed to listen on HTTP port: %v", err)
			}

			httpListener = passthrough.NewHTTPListener(httpLn, rtr, handler)
			log.Printf("starting HTTP passthrough on :%d", cfg.HTTPPort)
			if err := httpListener.Serve(); err != nil {
				log.Fatalf("HTTP listener error: %v", err)
			}
		} else {
			httpServer := &http.Server{
				Addr:    ":" + strconv.Itoa(cfg.HTTPPort),
				Handler: handler,
			}
			log.Printf("starting HTTP server on :%d", cfg.HTTPPort)
			if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
				log.Fatalf("HTTP server error: %v", err)
			}
		}
	}
}

// tlsHandler wraps an http.Handler with TLS termination
type tlsHandler struct {
	handler   http.Handler
	tlsConfig *tls.Config
}

func (h *tlsHandler) ServeHTTP(w http.ResponseWriter, r *http.Request) {
	h.handler.ServeHTTP(w, r)
}
