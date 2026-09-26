package main

import (
	"fmt"
	"net"
	"net/mail"
	"os"
	"strconv"
	"strings"
	"time"
)

type Config struct {
	// VLESS 入口配置
	VLESSListen string
	TLSMode     string // "acme" 或 "none"
	TLSDomain   string
	ACMEEmail   string

	// Spot 心跳与管理
	AgentToken            string
	HeartbeatTimeout      time.Duration
	SpotRetention         time.Duration
	FailbackStable        time.Duration
	MaxBackupSlotsPerSpot int

	// 槽位端口范围
	PortRangeStart int
	PortRangeEnd   int

	// 轮询与超时
	SyncInterval      time.Duration
	UpstreamSOCKSPort int
	DialTimeout       time.Duration

	// 管理 API / Web UI / 心跳监听
	AdminListen    string
	AdminToken     string
	DataPath       string
	AgentBinaryDir string
}

func loadConfig() (Config, error) {
	c := Config{
		VLESSListen:           env("VLESS_LISTEN", ":443"),
		TLSMode:               strings.ToLower(env("TLS_MODE", "acme")),
		TLSDomain:             strings.ToLower(env("TLS_DOMAIN", "")),
		ACMEEmail:             env("ACME_EMAIL", ""),
		AgentToken:            env("AGENT_TOKEN", ""),
		HeartbeatTimeout:      envDuration("HEARTBEAT_TIMEOUT", 30*time.Second),
		SpotRetention:         envDuration("SPOT_RETENTION", 72*time.Hour),
		FailbackStable:        envDuration("FAILBACK_STABLE", 60*time.Second),
		MaxBackupSlotsPerSpot: envInt("MAX_BACKUP_SLOTS_PER_SPOT", 2),
		PortRangeStart:        envInt("PORT_RANGE_START", 20000),
		PortRangeEnd:          envInt("PORT_RANGE_END", 20999),
		SyncInterval:          envDuration("SYNC_INTERVAL", 10*time.Second),
		UpstreamSOCKSPort:     envInt("UPSTREAM_SOCKS_PORT", 1080),
		DialTimeout:           envDuration("DIAL_TIMEOUT", 10*time.Second),
		AdminListen:           env("ADMIN_LISTEN", "0.0.0.0:9090"),
		AdminToken:            env("ADMIN_TOKEN", ""),
		DataPath:              env("DATA_PATH", "/var/lib/newspot-relay/state.json"),
		AgentBinaryDir:        env("AGENT_BINARY_DIR", "/var/lib/newspot-relay/bin"),
	}
	if err := c.validate(); err != nil {
		return Config{}, err
	}
	return c, nil
}

func (c Config) validate() error {
	if c.AgentToken == "" {
		return fmt.Errorf("AGENT_TOKEN is required")
	}
	if c.AdminToken == "" {
		return fmt.Errorf("ADMIN_TOKEN is required")
	}
	if _, _, err := net.SplitHostPort(c.VLESSListen); err != nil {
		return fmt.Errorf("invalid VLESS_LISTEN %q: %w", c.VLESSListen, err)
	}
	switch c.TLSMode {
	case "acme":
		if !validTLSDomain(c.TLSDomain) {
			return fmt.Errorf("invalid TLS_DOMAIN %q", c.TLSDomain)
		}
		parsed, err := mail.ParseAddress(c.ACMEEmail)
		if err != nil || parsed.Address != strings.TrimSpace(c.ACMEEmail) {
			return fmt.Errorf("invalid ACME_EMAIL %q", c.ACMEEmail)
		}
	case "none":
		// No TLS needed
	default:
		return fmt.Errorf("invalid TLS_MODE %q (must be 'acme' or 'none')", c.TLSMode)
	}

	if _, _, err := net.SplitHostPort(c.AdminListen); err != nil {
		return fmt.Errorf("invalid ADMIN_LISTEN %q: %w", c.AdminListen, err)
	}
	if c.PortRangeStart <= 0 || c.PortRangeEnd > 65535 || c.PortRangeStart > c.PortRangeEnd {
		return fmt.Errorf("invalid port range %d-%d", c.PortRangeStart, c.PortRangeEnd)
	}
	return nil
}

func validTLSDomain(domain string) bool {
	if domain == "" || len(domain) > 253 || strings.ContainsAny(domain, ":/\\@") || net.ParseIP(domain) != nil {
		return false
	}
	labels := strings.Split(domain, ".")
	if len(labels) < 2 {
		return false
	}
	for _, l := range labels {
		if len(l) == 0 || len(l) > 63 || l[0] == '-' || l[len(l)-1] == '-' {
			return false
		}
	}
	return true
}

func env(key, fallback string) string {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		return val
	}
	return fallback
}

func envInt(key string, fallback int) int {
	if val := strings.TrimSpace(os.Getenv(key)); val != "" {
		if n, err := strconv.Atoi(val); err == nil {
			return n
		}
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
