package main

import (
	"log"
	"sort"
	"sync"
	"time"
)

type nodeInfo struct {
	instanceName     string
	region           string
	ipv4             string // GCP internal IP
	upstreamPort     int
	publicIPVerified string
}

type target struct {
	Port         int       `json:"port"`
	Primary      string    `json:"primary_instance"`
	Serving      string    `json:"serving_instance"`
	ServingIPv4  string    `json:"serving_internal_ip"`
	UpstreamPort int       `json:"upstream_port"`
	ExitIP       string    `json:"exit_ip"`
	IsBackup     bool      `json:"is_backup"`
	PrimaryUp    bool      `json:"primary_healthy"`
	Reason       string    `json:"reason"`
	Since        time.Time `json:"since"`
}

type routeDecision struct {
	instance     string
	ipv4         string
	upstreamPort int
}

type pool struct {
	config Config
	store  *Store
	spots  *spotRegistry
	conns  *connRegistry

	mu      sync.RWMutex
	targets map[int]*target

	present             map[string]nodeInfo
	primaryHealthySince map[string]time.Time
	regionHint          map[string]string

	clock func() time.Time
}

func newPool(config Config, store *Store, spots *spotRegistry, conns *connRegistry) *pool {
	return &pool{
		config:              config,
		store:               store,
		spots:               spots,
		conns:               conns,
		targets:             make(map[int]*target),
		present:             make(map[string]nodeInfo),
		primaryHealthySince: make(map[string]time.Time),
		regionHint:          make(map[string]string),
		clock:               func() time.Time { return time.Now().UTC() },
	}
}

func (p *pool) syncOnce() {
	now := p.clock()
	healthySpots := p.spots.HealthySpots(p.config.HeartbeatTimeout, now)

	present := make(map[string]nodeInfo, len(healthySpots))
	for _, s := range healthySpots {
		port := s.SOCKSPort
		if port <= 0 {
			port = p.config.UpstreamSOCKSPort
		}
		present[s.InstanceName] = nodeInfo{
			instanceName:     s.InstanceName,
			region:           s.Region,
			ipv4:             s.InternalIP, // GCP 内网 IP!
			upstreamPort:     port,
			publicIPVerified: s.PublicIP,
		}
	}

	// 1. 为每个健康 spot 分配/确保 slot
	presentNames := make([]string, 0, len(present))
	for name := range present {
		presentNames = append(presentNames, name)
	}
	sort.Strings(presentNames)

	var touched []string
	for _, name := range presentNames {
		info := present[name]
		if _, _, err := p.store.EnsureSlot(name); err != nil {
			log.Printf("ensure slot for %s error: %v", name, err)
			continue
		}
		touched = append(touched, name)
		p.regionHint[name] = info.region
		if _, wasPresent := p.present[name]; !wasPresent {
			p.primaryHealthySince[name] = now
		}
	}
	p.store.TouchHealthyMany(touched, now)

	for name := range p.present {
		if _, ok := present[name]; !ok {
			delete(p.primaryHealthySince, name)
		}
	}

	// 2. 回收长期消失的端口
	slots := p.store.Slots()
	reclaimedAny := false
	for _, sl := range slots {
		if _, ok := present[sl.InstanceName]; ok {
			continue
		}
		if now.Sub(sl.LastHealthyAt) > p.config.SpotRetention {
			if p.store.ReclaimSlot(sl.Port) {
				log.Printf("reclaimed slot %d for inactive spot %s", sl.Port, sl.InstanceName)
				delete(p.regionHint, sl.InstanceName)
				delete(p.primaryHealthySince, sl.InstanceName)
				reclaimedAny = true
			}
		}
	}
	if reclaimedAny {
		_ = p.store.Save()
	}

	// 3. 计算服务决策
	slots = p.store.Slots()
	activeByInstance := p.conns.activeByInstance()
	backupUsage := make(map[string]int)
	newTargets := make(map[int]*target, len(slots))

	sort.Slice(slots, func(i, j int) bool { return slots[i].Port < slots[j].Port })
	for _, sl := range slots {
		prev := p.targetSnapshot(sl.Port)
		dec := p.decide(sl, present, prev, backupUsage, activeByInstance, now)
		newTargets[sl.Port] = dec
	}

	p.mu.Lock()
	p.targets = newTargets
	p.present = present
	p.mu.Unlock()
}

func (p *pool) decide(slot Slot, present map[string]nodeInfo, previous *target, backupUsage, activeByInstance map[string]int, now time.Time) *target {
	primary := slot.InstanceName
	result := &target{Port: slot.Port, Primary: primary, Since: now}
	if previous != nil {
		result.Since = previous.Since
	}

	primaryInfo, primaryUp := present[primary]
	result.PrimaryUp = primaryUp

	if primaryUp {
		servingPrimaryAlready := previous != nil && previous.Serving == primary
		healthySince, ok := p.primaryHealthySince[primary]
		stable := ok && now.Sub(healthySince) >= p.config.FailbackStable
		backupStillServing := previous != nil && previous.Serving != "" && previous.Serving != primary
		if _, backupUp := present[stringOrEmpty(previous)]; backupStillServing && !backupUp {
			backupStillServing = false
		}

		if servingPrimaryAlready || stable || !backupStillServing {
			p.assign(result, primary, primaryInfo, false, "primary healthy", previous, now)
			return result
		}
		backup := present[previous.Serving]
		backupUsage[previous.Serving]++
		p.assign(result, previous.Serving, backup, true, "failing back: waiting for primary to stabilize", previous, now)
		return result
	}

	// 主节点离线，优先保持现有备用
	if previous != nil && previous.Serving != "" && previous.Serving != primary {
		if info, up := present[previous.Serving]; up {
			if p.config.MaxBackupSlotsPerSpot <= 0 || backupUsage[previous.Serving] < p.config.MaxBackupSlotsPerSpot {
				backupUsage[previous.Serving]++
				p.assign(result, previous.Serving, info, true, "primary unavailable, served by backup", previous, now)
				return result
			}
		}
	}

	// 选取新备用
	backupName, backupInfo, ok := p.pickBackup(primary, present, backupUsage, activeByInstance)
	if !ok {
		result.Serving = ""
		result.Reason = "no healthy spot available"
		return result
	}
	backupUsage[backupName]++
	p.assign(result, backupName, backupInfo, true, "primary unavailable, served by backup", previous, now)
	return result
}

func (p *pool) assign(result *target, instance string, info nodeInfo, isBackup bool, reason string, previous *target, now time.Time) {
	if previous == nil || previous.Serving != instance {
		result.Since = now
	}
	result.Serving = instance
	result.ServingIPv4 = info.ipv4
	result.UpstreamPort = info.upstreamPort
	result.ExitIP = info.publicIPVerified
	result.IsBackup = isBackup
	result.Reason = reason
}

func (p *pool) pickBackup(primary string, present map[string]nodeInfo, backupUsage, activeByInstance map[string]int) (string, nodeInfo, bool) {
	primaryRegion := p.regionHint[primary]
	var candidates []nodeInfo
	for name, info := range present {
		if name == primary {
			continue
		}
		if p.config.MaxBackupSlotsPerSpot > 0 && backupUsage[name] >= p.config.MaxBackupSlotsPerSpot {
			continue
		}
		candidates = append(candidates, info)
	}
	if len(candidates) == 0 {
		return "", nodeInfo{}, false
	}
	sort.Slice(candidates, func(i, j int) bool {
		a, b := candidates[i], candidates[j]
		aSame := primaryRegion != "" && a.region == primaryRegion
		bSame := primaryRegion != "" && b.region == primaryRegion
		if aSame != bSame {
			return aSame
		}
		if backupUsage[a.instanceName] != backupUsage[b.instanceName] {
			return backupUsage[a.instanceName] < backupUsage[b.instanceName]
		}
		if activeByInstance[a.instanceName] != activeByInstance[b.instanceName] {
			return activeByInstance[a.instanceName] < activeByInstance[b.instanceName]
		}
		return a.instanceName < b.instanceName
	})
	chosen := candidates[0]
	return chosen.instanceName, chosen, true
}

func (p *pool) route(port int) (routeDecision, bool) {
	p.mu.RLock()
	defer p.mu.RUnlock()
	current, ok := p.targets[port]
	if ok && current.Serving != "" && current.ServingIPv4 != "" {
		return routeDecision{
			instance:     current.Serving,
			ipv4:         current.ServingIPv4,
			upstreamPort: current.UpstreamPort,
		}, true
	}
	// 容错后备：若指定槽位暂无分配（例如用户绑定未初始化的槽位），降级使用当前任意健康运行的 Spot
	for _, t := range p.targets {
		if t.Serving != "" && t.ServingIPv4 != "" {
			return routeDecision{
				instance:     t.Serving,
				ipv4:         t.ServingIPv4,
				upstreamPort: t.UpstreamPort,
			}, true
		}
	}
	return routeDecision{}, false
}

func (p *pool) statusTargets() []target {
	p.mu.RLock()
	defer p.mu.RUnlock()
	var res []target
	for _, t := range p.targets {
		res = append(res, *t)
	}
	sort.Slice(res, func(i, j int) bool { return res[i].Port < res[j].Port })
	return res
}

func (p *pool) targetSnapshot(port int) *target {
	p.mu.RLock()
	defer p.mu.RUnlock()
	curr, ok := p.targets[port]
	if !ok {
		return nil
	}
	clone := *curr
	return &clone
}

func stringOrEmpty(t *target) string {
	if t == nil {
		return ""
	}
	return t.Serving
}

func (p *pool) removeSpot(instanceName string) {
	p.mu.Lock()
	delete(p.present, instanceName)
	delete(p.regionHint, instanceName)
	delete(p.primaryHealthySince, instanceName)
	p.mu.Unlock()
	p.syncOnce()
}

func (p *pool) removeSlot(port int) {
	p.mu.Lock()
	delete(p.targets, port)
	p.mu.Unlock()
	p.syncOnce()
}
