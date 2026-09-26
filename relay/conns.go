package main

import "sync"

// liveConn 只记录池调度和状态页需要的逻辑槽位与当前承载 Spot。
type liveConn struct {
	id       uint64
	slot     int
	instance string
}

// connRegistry 跟踪活动 VLESS TCP 会话，供备用选择与状态查询使用。
type connRegistry struct {
	mu     sync.RWMutex
	next   uint64
	byID   map[uint64]liveConn
	bySlot map[int]int
	byInst map[string]int
}

func newConnRegistry() *connRegistry {
	return &connRegistry{
		byID:   map[uint64]liveConn{},
		bySlot: map[int]int{},
		byInst: map[string]int{},
	}
}

func (registry *connRegistry) add(slot int, instance string) uint64 {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	registry.next++
	connection := liveConn{id: registry.next, slot: slot, instance: instance}
	registry.byID[connection.id] = connection
	registry.bySlot[slot]++
	registry.byInst[instance]++
	return connection.id
}

func (registry *connRegistry) remove(id uint64) {
	registry.mu.Lock()
	defer registry.mu.Unlock()
	connection, ok := registry.byID[id]
	if !ok {
		return
	}
	delete(registry.byID, id)
	decrement(registry.bySlot, connection.slot)
	decrement(registry.byInst, connection.instance)
}

func decrement[K comparable](counts map[K]int, key K) {
	if counts[key] <= 1 {
		delete(counts, key)
		return
	}
	counts[key]--
}

// totalActive 返回当前所有活动的会话总数。
func (registry *connRegistry) totalActive() int {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return len(registry.byID)
}

// activeByInstance 返回每个 Spot 当前承载的连接数，供备用选择使用。
func (registry *connRegistry) activeByInstance() map[string]int {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return copyCounts(registry.byInst)
}

// countsByPort 返回每个内部槽位当前承载的会话数。
func (registry *connRegistry) countsByPort() map[int]int {
	registry.mu.RLock()
	defer registry.mu.RUnlock()
	return copyCounts(registry.bySlot)
}

func copyCounts[K comparable](source map[K]int) map[K]int {
	result := make(map[K]int, len(source))
	for key, value := range source {
		result[key] = value
	}
	return result
}
