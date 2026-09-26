package main

import (
	"testing"
	"time"
)

func TestSpotRegistryHeartbeatAndPrune(t *testing.T) {
	reg := newSpotRegistry()
	now := time.Now().UTC()

	spotA := SpotInfo{
		InstanceName: "spot-a",
		Region:       "asia-east1",
		Zone:         "asia-east1-a",
		InternalIP:   "10.140.0.10",
		PublicIP:     "34.80.1.1",
		SOCKSPort:    1080,
	}

	isNew := reg.Heartbeat(spotA, now)
	if !isNew {
		t.Fatalf("expected new spot registration")
	}

	// 第二次心跳不是新节点
	isNew = reg.Heartbeat(spotA, now.Add(2*time.Second))
	if isNew {
		t.Fatalf("expected existing spot")
	}

	healthy := reg.HealthySpots(10*time.Second, now.Add(5*time.Second))
	if len(healthy) != 1 {
		t.Fatalf("expected 1 healthy spot, got %d", len(healthy))
	}

	// 超时测试
	healthy = reg.HealthySpots(10*time.Second, now.Add(20*time.Second))
	if len(healthy) != 0 {
		t.Fatalf("expected 0 healthy spots on timeout, got %d", len(healthy))
	}

	// 清理测试
	pruned := reg.Prune(1*time.Hour, now.Add(2*time.Hour))
	if pruned != 1 {
		t.Fatalf("expected 1 pruned spot, got %d", pruned)
	}
}
