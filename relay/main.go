package main

import (
	"context"
	"crypto/tls"
	"errors"
	"log"
	"net"
	"net/http"
	"os"
	"os/signal"
	"syscall"
	"time"
)

func main() {
	if len(os.Args) > 1 && (os.Args[1] == "update" || os.Args[1] == "upgrade") {
		runSelfUpdate()
		return
	}

	config, err := loadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	store, err := newStore(config.DataPath, config.PortRangeStart, config.PortRangeEnd)
	if err != nil {
		log.Fatalf("state store error: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	spots := newSpotRegistry()
	conns := newConnRegistry()
	users := newVLESSUsers()
	users.LoadFromStore(store.Users())

	relayPool := newPool(config, store, spots, conns)

	// 1. 初始化 TLS 配置 (若启用 ACME)
	var tlsConf *tls.Config
	var acmeHTTP *http.Server

	if config.TLSMode == "acme" {
		manager := newAutocertManager(config)
		tlsConf = tlsConfig(manager)

		acmeHTTP = newACMEHTTPServer(manager)
		acmeListener, err := net.Listen("tcp", acmeHTTP.Addr)
		if err != nil {
			log.Fatalf("listen ACME HTTP on %s: %v", acmeHTTP.Addr, err)
		}
		go func() {
			if err := acmeHTTP.Serve(acmeListener); err != nil && !errors.Is(err, http.ErrServerClosed) {
				log.Printf("ACME HTTP error: %v", err)
			}
		}()
	}

	// 2. 启动管理与心跳 API + Web UI 服务器 (支持 HTTP 和 HTTPS 自适应)
	admin := newAdminServer(config, spots, relayPool, users, conns, store, tlsConf)
	go func() {
		log.Printf("Admin API & Web UI listening on %s (HTTP & HTTPS)", config.AdminListen)
		if err := admin.ListenAndServe(); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("admin server error: %v", err)
		}
	}()

	// 3. 启动 VLESS 监听器
	var vlessListener net.Listener
	if config.TLSMode == "acme" && tlsConf != nil {
		vlessListener, err = tls.Listen("tcp", config.VLESSListen, tlsConf)
		if err != nil {
			log.Fatalf("listen VLESS/TLS on %s: %v", config.VLESSListen, err)
		}
		log.Printf("newspot-relay started: VLESS/TLS %s (domain: %s)", config.VLESSListen, config.TLSDomain)
	} else {
		vlessListener, err = net.Listen("tcp", config.VLESSListen)
		if err != nil {
			log.Fatalf("listen VLESS/TCP on %s: %v", config.VLESSListen, err)
		}
		log.Printf("newspot-relay started: VLESS/TCP (no TLS) on %s", config.VLESSListen)
	}

	vless := newVLESSServer(config, conns, users, relayPool.route)
	go func() {
		if err := vless.Serve(vlessListener); err != nil {
			log.Printf("VLESS server error: %v", err)
		}
	}()

	// 3. 定期同步与过期清理
	go func() {
		ticker := time.NewTicker(config.SyncInterval)
		defer ticker.Stop()
		for {
			select {
			case <-ctx.Done():
				return
			case <-ticker.C:
				relayPool.syncOnce()
				now := time.Now().UTC()
				if pruned := spots.Prune(config.SpotRetention, now); pruned > 0 {
					log.Printf("pruned %d expired spots", pruned)
					_ = store.Save()
				}
			}
		}
	}()

	<-ctx.Done()
	log.Printf("shutting down newspot-relay...")
	_ = vless.Shutdown()
	shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	_ = admin.http.Shutdown(shutdownCtx)
	if acmeHTTP != nil {
		_ = acmeHTTP.Shutdown(shutdownCtx)
	}
}

func newACMEHTTPServer(manager interface {
	HTTPHandler(http.Handler) http.Handler
}) *http.Server {
	return &http.Server{
		Addr:              ":80",
		Handler:           manager.HTTPHandler(nil),
		ReadHeaderTimeout: 10 * time.Second,
	}
}
