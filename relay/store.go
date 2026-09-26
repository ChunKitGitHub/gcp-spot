package main

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

type Slot struct {
	Port          int       `json:"port"`
	InstanceName  string    `json:"instance_name"`
	LastHealthyAt time.Time `json:"last_healthy_at"`
}

type UserRecord struct {
	UUID      string    `json:"uuid"`
	Name      string    `json:"name"`
	SlotPort  int       `json:"slot_port"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

type StoreData struct {
	Slots []Slot       `json:"slots"`
	Users []UserRecord `json:"users"`
}

type Store struct {
	path      string
	rangeFrom int
	rangeTo   int

	mu    sync.Mutex
	slots map[int]Slot
	users map[string]UserRecord
}

func newStore(path string, rangeFrom, rangeTo int) (*Store, error) {
	s := &Store{
		path:      path,
		rangeFrom: rangeFrom,
		rangeTo:   rangeTo,
		slots:     make(map[int]Slot),
		users:     make(map[string]UserRecord),
	}
	if err := s.load(); err != nil && !os.IsNotExist(err) {
		return nil, fmt.Errorf("load store: %w", err)
	}
	return s, nil
}

func (s *Store) load() error {
	data, err := os.ReadFile(s.path)
	if err != nil {
		return err
	}
	var stored StoreData
	if err := json.Unmarshal(data, &stored); err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()

	for _, slot := range stored.Slots {
		s.slots[slot.Port] = slot
	}
	for _, u := range stored.Users {
		s.users[u.UUID] = u
	}
	return nil
}

func (s *Store) Save() error {
	s.mu.Lock()
	defer s.mu.Unlock()

	var data StoreData
	for _, sl := range s.slots {
		data.Slots = append(data.Slots, sl)
	}
	for _, u := range s.users {
		data.Users = append(data.Users, u)
	}

	payload, err := json.MarshalIndent(data, "", "  ")
	if err != nil {
		return err
	}

	dir := filepath.Dir(s.path)
	if err := os.MkdirAll(dir, 0755); err != nil {
		return err
	}

	tmpFile := s.path + ".tmp"
	if err := os.WriteFile(tmpFile, payload, 0600); err != nil {
		return err
	}
	return os.Rename(tmpFile, s.path)
}

func (s *Store) EnsureSlot(instanceName string) (int, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	for port, slot := range s.slots {
		if slot.InstanceName == instanceName {
			return port, false, nil
		}
	}

	// 分配新端口
	for p := s.rangeFrom; p <= s.rangeTo; p++ {
		if _, taken := s.slots[p]; !taken {
			s.slots[p] = Slot{
				Port:          p,
				InstanceName:  instanceName,
				LastHealthyAt: time.Now().UTC(),
			}
			return p, true, nil
		}
	}
	return 0, false, fmt.Errorf("slot port range exhausted (%d-%d)", s.rangeFrom, s.rangeTo)
}

func (s *Store) Slots() []Slot {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := make([]Slot, 0, len(s.slots))
	for _, sl := range s.slots {
		res = append(res, sl)
	}
	return res
}

func (s *Store) TouchHealthyMany(names []string, now time.Time) {
	s.mu.Lock()
	defer s.mu.Unlock()
	for port, slot := range s.slots {
		for _, name := range names {
			if slot.InstanceName == name {
				slot.LastHealthyAt = now
				s.slots[port] = slot
				break
			}
		}
	}
}

func (s *Store) ReclaimSlot(port int) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.slots[port]; ok {
		delete(s.slots, port)
		return true
	}
	return false
}

func (s *Store) Users() []UserRecord {
	s.mu.Lock()
	defer s.mu.Unlock()
	res := make([]UserRecord, 0, len(s.users))
	for _, u := range s.users {
		res = append(res, u)
	}
	return res
}

func (s *Store) PutUser(u UserRecord) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.users[u.UUID] = u
}

func (s *Store) DeleteUser(uuid string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	if _, ok := s.users[uuid]; ok {
		delete(s.users, uuid)
		return true
	}
	return false
}
