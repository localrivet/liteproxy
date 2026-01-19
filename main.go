package main

import (
	"context"
	"log"
	"net/http"
	"os"
	"os/signal"
	"strconv"
	"sync"
	"syscall"
	"time"

	"github.com/localrivet/liteproxy/compose"
	"github.com/localrivet/liteproxy/proxy"
	"github.com/localrivet/liteproxy/router"
	"github.com/localrivet/liteproxy/tls"
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
		log.Printf("  %s%s -> %s:%d", r.Host, r.PathPrefix, r.ServiceName, r.ServicePort)
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

	// State for hot reload
	var (
		mu          sync.Mutex
		certManager *autocert.Manager
		httpServer  *http.Server
		httpsServer *http.Server
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

		log.Printf("reloaded %d routes", len(newRoutes))
		for _, r := range newRoutes {
			log.Printf("  %s%s -> %s:%d", r.Host, r.PathPrefix, r.ServiceName, r.ServicePort)
		}

		// Update TLS hosts if HTTPS is enabled
		if cfg.HTTPSEnabled && certManager != nil {
			hosts := newRouter.Hosts()
			certManager = tls.UpdateHosts(certManager, hosts)
			if httpsServer != nil {
				httpsServer.TLSConfig = tls.TLSConfig(certManager)
			}
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
				ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
				defer cancel()

				mu.Lock()
				if httpServer != nil {
					httpServer.Shutdown(ctx)
				}
				if httpsServer != nil {
					httpsServer.Shutdown(ctx)
				}
				mu.Unlock()
				os.Exit(0)
			}
		}
	}()

	// Start servers
	if cfg.HTTPSEnabled {
		hosts := rtr.Hosts()
		certManager = tls.Manager(tls.Config{
			Email:    cfg.ACMEEmail,
			CacheDir: cfg.ACMEDir,
			Hosts:    hosts,
		})

		// HTTPS server
		httpsServer = &http.Server{
			Addr:      ":" + strconv.Itoa(cfg.HTTPSPort),
			Handler:   handler,
			TLSConfig: tls.TLSConfig(certManager),
		}

		// HTTP server for ACME challenges + redirect to HTTPS
		httpServer = &http.Server{
			Addr: ":" + strconv.Itoa(cfg.HTTPPort),
			Handler: certManager.HTTPHandler(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				// Redirect HTTP to HTTPS
				target := "https://" + r.Host + r.URL.RequestURI()
				http.Redirect(w, r, target, http.StatusMovedPermanently)
			})),
		}

		go func() {
			log.Printf("starting HTTP server on :%d (ACME challenges + HTTPS redirect)", cfg.HTTPPort)
			if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
				log.Fatalf("HTTP server error: %v", err)
			}
		}()

		log.Printf("starting HTTPS server on :%d", cfg.HTTPSPort)
		if err := httpsServer.ListenAndServeTLS("", ""); err != http.ErrServerClosed {
			log.Fatalf("HTTPS server error: %v", err)
		}
	} else {
		// HTTP only
		httpServer = &http.Server{
			Addr:    ":" + strconv.Itoa(cfg.HTTPPort),
			Handler: handler,
		}

		log.Printf("starting HTTP server on :%d", cfg.HTTPPort)
		if err := httpServer.ListenAndServe(); err != http.ErrServerClosed {
			log.Fatalf("HTTP server error: %v", err)
		}
	}
}
