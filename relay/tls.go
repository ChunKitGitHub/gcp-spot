package main

import (
	"crypto/tls"
	"path/filepath"

	"golang.org/x/crypto/acme/autocert"
)

func newAutocertManager(config Config) *autocert.Manager {
	return &autocert.Manager{
		Prompt:     autocert.AcceptTOS,
		Cache:      autocert.DirCache(filepath.Join(filepath.Dir(config.DataPath), "acme")),
		HostPolicy: autocert.HostWhitelist(config.TLSDomain),
		Email:      config.ACMEEmail,
	}
}

func tlsConfig(manager *autocert.Manager) *tls.Config {
	config := manager.TLSConfig()
	config.MinVersion = tls.VersionTLS12
	return config
}
