package main

import (
	"fmt"
	"net"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	RelayURL          string        // 中转机地址，如 http://10.140.0.2:9090
	AgentToken        string        // 认证 Token
	SOCKSListen       string        // SOCKS5 监听地址，默认 0.0.0.0:1080
	HeartbeatInterval time.Duration // 心跳间隔，默认 10s
	DialTimeout       time.Duration // 出网连接超时，默认 10s
}

func loadConfig() (Config, error) {
	c := Config{
		RelayURL:          strings.TrimRight(env("RELAY_URL", ""), "/"),
		AgentToken:        env("AGENT_TOKEN", ""),
		SOCKSListen:       env("SOCKS_LISTEN", "0.0.0.0:1080"),
		HeartbeatInterval: envDuration("HEARTBEAT_INTERVAL", 10*time.Second),
		DialTimeout:       envDuration("DIAL_TIMEOUT", 10*time.Second),
	}
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) validate() error {
	if c.RelayURL == "" {
		return fmt.Errorf("RELAY_URL is required (e.g. http://10.140.0.2:9090)")
	}
	if c.AgentToken == "" {
		return fmt.Errorf("AGENT_TOKEN is required")
	}
	if _, _, err := net.SplitHostPort(c.SOCKSListen); err != nil {
		return fmt.Errorf("invalid SOCKS_LISTEN %q: %w", c.SOCKSListen, err)
	}
	return nil
}

func env(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

func envDuration(key string, fallback time.Duration) time.Duration {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		if d, err := time.ParseDuration(val); err == nil {
			return d
		}
	}
	return fallback
}

func parsePort(addr string) int {
	_, p, err := net.SplitHostPort(addr)
	if err != nil {
		return 1080
	}
	port, err := strconv.Atoi(p)
	if err != nil {
		return 1080
	}
	return port
}
