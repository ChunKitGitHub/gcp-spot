package main

import (
	"bytes"
	"context"
	"encoding/json"
	"io"
	"log"
	"net/http"
	"time"
)

type HeartbeatPayload struct {
	InstanceName string `json:"instance_name"`
	Region       string `json:"region"`
	Zone         string `json:"zone"`
	InternalIP   string `json:"internal_ip"`
	PublicIP     string `json:"public_ip"`
	SOCKSPort    int    `json:"socks_port"`
}

type HeartbeatClient struct {
	relayURL  string
	token     string
	meta      SpotMetadata
	socksPort int
	interval  time.Duration
	client    *http.Client
}

func NewHeartbeatClient(relayURL, token string, meta SpotMetadata, socksPort int, interval time.Duration) *HeartbeatClient {
	return &HeartbeatClient{
		relayURL:  relayURL,
		token:     token,
		meta:      meta,
		socksPort: socksPort,
		interval:  interval,
		client:    &http.Client{Timeout: 5 * time.Second},
	}
}

func (h *HeartbeatClient) Run(ctx context.Context) {
	ticker := time.NewTicker(h.interval)
	defer ticker.Stop()

	// 定期重新刷新公网 IP（每 5 分钟）
	ipRefreshTicker := time.NewTicker(5 * time.Minute)
	defer ipRefreshTicker.Stop()

	// 立即发送第一次心跳
	h.send(ctx)

	for {
		select {
		case <-ctx.Done():
			return
		case <-ipRefreshTicker.C:
			newIP := fetchPublicIP(ctx)
			if newIP != "" && newIP != h.meta.PublicIP {
				log.Printf("public IP changed: %s -> %s", h.meta.PublicIP, newIP)
				h.meta.PublicIP = newIP
			}
		case <-ticker.C:
			h.send(ctx)
		}
	}
}

func (h *HeartbeatClient) send(ctx context.Context) {
	payload := HeartbeatPayload{
		InstanceName: h.meta.InstanceName,
		Region:       h.meta.Region,
		Zone:         h.meta.Zone,
		InternalIP:   h.meta.InternalIP,
		PublicIP:     h.meta.PublicIP,
		SOCKSPort:    h.socksPort,
	}

	data, err := json.Marshal(payload)
	if err != nil {
		log.Printf("marshal heartbeat error: %v", err)
		return
	}

	url := h.relayURL + "/api/heartbeat"
	req, err := http.NewRequestWithContext(ctx, "POST", url, bytes.NewReader(data))
	if err != nil {
		log.Printf("create heartbeat req error: %v", err)
		return
	}

	req.Header.Set("Authorization", "Bearer "+h.token)
	req.Header.Set("Content-Type", "application/json")

	resp, err := h.client.Do(req)
	if err != nil {
		log.Printf("heartbeat to %s failed: %v", url, err)
		return
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusNoContent {
		body, _ := io.ReadAll(io.LimitReader(resp.Body, 1024))
		log.Printf("heartbeat to %s returned %d: %s", url, resp.StatusCode, string(body))
	}
}
