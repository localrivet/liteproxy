package tls

import (
	"crypto/tls"
	"log"

	"golang.org/x/crypto/acme/autocert"
)

// Config holds TLS configuration
type Config struct {
	Email    string   // ACME account email
	CacheDir string   // Directory to store certificates
	Hosts    []string // Allowed hosts for certificate issuance
}

// Manager creates an autocert manager for automatic Let's Encrypt certificates
func Manager(cfg Config) *autocert.Manager {
	return &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Email:      cfg.Email,
		Cache:      autocert.DirCache(cfg.CacheDir),
		HostPolicy: autocert.HostWhitelist(cfg.Hosts...),
	}
}

// TLSConfig returns a tls.Config using the autocert manager
func TLSConfig(m *autocert.Manager) *tls.Config {
	return &tls.Config{
		GetCertificate: m.GetCertificate,
		NextProtos:     []string{"h2", "http/1.1"},
		MinVersion:     tls.VersionTLS12,
	}
}

// UpdateHosts creates a new manager with updated hosts
// This is called when the compose file is reloaded
func UpdateHosts(m *autocert.Manager, hosts []string) *autocert.Manager {
	log.Printf("updating TLS hosts: %v", hosts)
	return &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Email:      m.Email,
		Cache:      m.Cache,
		HostPolicy: autocert.HostWhitelist(hosts...),
	}
}
