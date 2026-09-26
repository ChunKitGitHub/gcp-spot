package main

import (
	"sync"
	"time"
)

type SpotInfo struct {
	InstanceName string    `json:"instance_name"`
	Region       string    `json:"region"`
	Zone         string    `json:"zone"`
	InternalIP   string    `json:"internal_ip"`
	PublicIP     string    `json:"public_ip"`
	SOCKSPort    int       `json:"socks_port"`
	LastSeenAt   time.Time `json:"last_seen_at"`
	IsHealthy    bool      `json:"is_healthy"`
}

type spotRegistry struct {
	mu    sync.RWMutex
	spots map[string]*SpotInfo
}

func newSpotRegistry() *spotRegistry {
	return &spotRegistry{
		spots: make(map[string]*SpotInfo),
	}
}

func (r *spotRegistry) Heartbeat(info SpotInfo, now time.Time) bool {
	r.mu.Lock()
	defer r.mu.Unlock()

	existing, exists := r.spots[info.InstanceName]
	if !exists {
		info.LastSeenAt = now
		info.IsHealthy = true
		r.spots[info.InstanceName] = &info
		return true // 新节点
	}

	existing.Region = info.Region
	existing.Zone = info.Zone
	existing.InternalIP = info.InternalIP
	existing.PublicIP = info.PublicIP
	existing.SOCKSPort = info.SOCKSPort
	existing.LastSeenAt = now
	existing.IsHealthy = true
	return false
}

func (r *spotRegistry) HealthySpots(timeout time.Duration, now time.Time) []SpotInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []SpotInfo
	for _, s := range r.spots {
		if now.Sub(s.LastSeenAt) <= timeout {
			clone := *s
			clone.IsHealthy = true
			result = append(result, clone)
		}
	}
	return result
}

func (r *spotRegistry) AllSpots(timeout time.Duration, now time.Time) []SpotInfo {
	r.mu.RLock()
	defer r.mu.RUnlock()

	var result []SpotInfo
	for _, s := range r.spots {
		clone := *s
		clone.IsHealthy = now.Sub(s.LastSeenAt) <= timeout
		result = append(result, clone)
	}
	return result
}

func (r *spotRegistry) Prune(retention time.Duration, now time.Time) int {
	r.mu.Lock()
	defer r.mu.Unlock()

	count := 0
	for name, s := range r.spots {
		if now.Sub(s.LastSeenAt) > retention {
			delete(r.spots, name)
			count++
		}
	}
	return count
}
