package main

import (
	"path/filepath"
	"testing"
	"time"
)

func TestPoolFailoverAndFailback(t *testing.T) {
	tmpDir := t.TempDir()
	store, err := newStore(filepath.Join(tmpDir, "state.json"), 20000, 20005)
	if err != nil {
		t.Fatalf("new store: %v", err)
	}

	cfg := Config{
		HeartbeatTimeout:      10 * time.Second,
		SpotRetention:         1 * time.Hour,
		FailbackStable:        30 * time.Second,
		MaxBackupSlotsPerSpot: 2,
		UpstreamSOCKSPort:     1080,
	}

	spots := newSpotRegistry()
	conns := newConnRegistry()
	now := time.Date(2026, 9, 26, 10, 0, 0, 0, time.UTC)

	p := newPool(cfg, store, spots, conns)
	p.clock = func() time.Time { return now }

	// 1. 注册 Spot A
	spots.Heartbeat(SpotInfo{
		InstanceName: "spot-a",
		Region:       "asia-east1",
		InternalIP:   "10.140.0.1",
		SOCKSPort:    1080,
		PublicIP:     "34.80.0.1",
	}, now)

	// 注册 Spot B (备用)
	spots.Heartbeat(SpotInfo{
		InstanceName: "spot-b",
		Region:       "asia-east1",
		InternalIP:   "10.140.0.2",
		SOCKSPort:    1080,
		PublicIP:     "34.80.0.2",
	}, now)

	p.syncOnce()

	slot20000 := p.targetSnapshot(20000)
	if slot20000 == nil || slot20000.Serving != "spot-a" {
		t.Fatalf("slot 20000 should serve spot-a, got: %+v", slot20000)
	}

	// 2. Spot A 失联 (经过 15 秒，未收到 A 心跳，只有 B)
	now = now.Add(15 * time.Second)
	spots.Heartbeat(SpotInfo{
		InstanceName: "spot-b",
		Region:       "asia-east1",
		InternalIP:   "10.140.0.2",
		SOCKSPort:    1080,
		PublicIP:     "34.80.0.2",
	}, now)

	p.syncOnce()

	slot20000 = p.targetSnapshot(20000)
	if slot20000.Serving != "spot-b" || !slot20000.IsBackup {
		t.Fatalf("slot 20000 should failover to spot-b, got: %+v", slot20000)
	}

	// 3. Spot A 恢复，但未达到 FailbackStable (30s)
	now = now.Add(10 * time.Second)
	spots.Heartbeat(SpotInfo{
		InstanceName: "spot-a",
		Region:       "asia-east1",
		InternalIP:   "10.140.0.1",
		SOCKSPort:    1080,
		PublicIP:     "34.80.0.1",
	}, now)
	spots.Heartbeat(SpotInfo{
		InstanceName: "spot-b",
		Region:       "asia-east1",
		InternalIP:   "10.140.0.2",
		SOCKSPort:    1080,
		PublicIP:     "34.80.0.2",
	}, now)

	p.syncOnce()

	slot20000 = p.targetSnapshot(20000)
	if slot20000.Serving != "spot-b" {
		t.Fatalf("slot 20000 should stay on spot-b during stabilization, got: %+v", slot20000)
	}

	// 4. Spot A 持续稳定超过 FailbackStable (再过 35s)
	now = now.Add(35 * time.Second)
	spots.Heartbeat(SpotInfo{
		InstanceName: "spot-a",
		Region:       "asia-east1",
		InternalIP:   "10.140.0.1",
		SOCKSPort:    1080,
		PublicIP:     "34.80.0.1",
	}, now)
	spots.Heartbeat(SpotInfo{
		InstanceName: "spot-b",
		Region:       "asia-east1",
		InternalIP:   "10.140.0.2",
		SOCKSPort:    1080,
		PublicIP:     "34.80.0.2",
	}, now)

	p.syncOnce()

	slot20000 = p.targetSnapshot(20000)
	if slot20000.Serving != "spot-a" || slot20000.IsBackup {
		t.Fatalf("slot 20000 should failback to spot-a, got: %+v", slot20000)
	}
}
