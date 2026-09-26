package main

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/http"
	"os"
	"strings"
	"time"
)

type SpotMetadata struct {
	InstanceName string
	Region       string
	Zone         string
	InternalIP   string
	PublicIP     string
}

func GatherMetadata(ctx context.Context) (SpotMetadata, error) {
	meta := SpotMetadata{}

	// 1. 尝试从 GCP Metadata 服务读取
	gcpMeta, err := fetchGCPMetadata(ctx)
	if err == nil {
		meta = gcpMeta
	} else {
		// 非 GCP 环境降级
		hostname, _ := os.Hostname()
		meta.InstanceName = hostname
		meta.Region = "default"
		meta.Zone = "default-a"
		meta.InternalIP = getFirstNonLoopbackIPv4()
	}

	// 2. 检测公网出网 IP
	meta.PublicIP = fetchPublicIP(ctx)

	if meta.InternalIP == "" {
		return meta, fmt.Errorf("could not determine internal IPv4 address")
	}
	return meta, nil
}

func fetchGCPMetadata(ctx context.Context) (SpotMetadata, error) {
	client := &http.Client{Timeout: 3 * time.Second}
	baseURL := "http://metadata.google.internal/computeMetadata/v1"

	req := func(path string) (string, error) {
		r, err := http.NewRequestWithContext(ctx, "GET", baseURL+path, nil)
		if err != nil {
			return "", err
		}
		r.Header.Set("Metadata-Flavor", "Google")
		resp, err := client.Do(r)
		if err != nil {
			return "", err
		}
		defer resp.Body.Close()
		if resp.StatusCode != http.StatusOK {
			return "", fmt.Errorf("metadata HTTP %d", resp.StatusCode)
		}
		body, err := io.ReadAll(resp.Body)
		if err != nil {
			return "", err
		}
		return strings.TrimSpace(string(body)), nil
	}

	name, err := req("/instance/name")
	if err != nil {
		return SpotMetadata{}, err
	}
	zoneRaw, err := req("/instance/zone")
	if err != nil {
		return SpotMetadata{}, err
	}
	// zoneRaw 格式类似于 projects/12345/zones/asia-east1-b
	zone := zoneRaw
	if idx := strings.LastIndex(zoneRaw, "/"); idx != -1 {
		zone = zoneRaw[idx+1:]
	}
	region := zone
	if idx := strings.LastIndex(zone, "-"); idx != -1 {
		region = zone[:idx]
	}

	internalIP, err := req("/instance/network-interfaces/0/ip")
	if err != nil {
		return SpotMetadata{}, err
	}

	return SpotMetadata{
		InstanceName: name,
		Region:       region,
		Zone:         zone,
		InternalIP:   internalIP,
	}, nil
}

func fetchPublicIP(ctx context.Context) string {
	client := &http.Client{Timeout: 5 * time.Second}
	req, err := http.NewRequestWithContext(ctx, "GET", "https://api4.ipify.org", nil)
	if err != nil {
		return ""
	}
	resp, err := client.Do(req)
	if err != nil {
		return ""
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return ""
	}
	ip := strings.TrimSpace(string(body))
	if net.ParseIP(ip) != nil {
		return ip
	}
	return ""
}

func getFirstNonLoopbackIPv4() string {
	interfaces, err := net.Interfaces()
	if err != nil {
		return "127.0.0.1"
	}
	for _, iface := range interfaces {
		if iface.Flags&net.FlagUp == 0 || iface.Flags&net.FlagLoopback != 0 {
			continue
		}
		addrs, err := iface.Addrs()
		if err != nil {
			continue
		}
		for _, addr := range addrs {
			ipNet, ok := addr.(*net.IPNet)
			if ok && !ipNet.IP.IsLoopback() {
				if ipNet.IP.To4() != nil {
					return ipNet.IP.String()
				}
			}
		}
	}
	return "127.0.0.1"
}
