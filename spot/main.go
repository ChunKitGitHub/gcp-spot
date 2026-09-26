package main

import (
	"context"
	"log"
	"os/signal"
	"syscall"
)

func main() {
	config, err := loadConfig()
	if err != nil {
		log.Fatalf("config error: %v", err)
	}

	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()

	log.Printf("spot-agent starting: gathering metadata...")
	meta, err := GatherMetadata(ctx)
	if err != nil {
		log.Fatalf("gather metadata failed: %v", err)
	}
	log.Printf("metadata gathered: instance=%s region=%s zone=%s internal_ip=%s public_ip=%s",
		meta.InstanceName, meta.Region, meta.Zone, meta.InternalIP, meta.PublicIP)

	socksPort := parsePort(config.SOCKSListen)
	socksServer := NewSOCKS5Server(config.SOCKSListen, config.DialTimeout)

	go func() {
		log.Printf("SOCKS5 server listening on %s", config.SOCKSListen)
		if err := socksServer.ListenAndServe(); err != nil {
			log.Printf("SOCKS5 server stopped: %v", err)
		}
	}()

	hbClient := NewHeartbeatClient(config.RelayURL, config.AgentToken, meta, socksPort, config.HeartbeatInterval)
	go hbClient.Run(ctx)
	log.Printf("heartbeat client started -> %s (interval: %s)", config.RelayURL, config.HeartbeatInterval)

	<-ctx.Done()
	log.Printf("shutting down spot-agent...")
	_ = socksServer.Close()
}
