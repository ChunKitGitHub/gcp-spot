package main

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
	"net"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

type VLESSUser struct {
	UUID      string    `json:"uuid"`
	Name      string    `json:"name"`
	SlotPort  int       `json:"slot_port"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
}

type vlessUsers struct {
	mu    sync.RWMutex
	users map[[16]byte]*VLESSUser
	slots map[int][16]byte
}

func newVLESSUsers() *vlessUsers {
	return &vlessUsers{
		users: make(map[[16]byte]*VLESSUser),
		slots: make(map[int][16]byte),
	}
}

func (u *vlessUsers) Slot(uuid [16]byte) (int, bool) {
	u.mu.RLock()
	defer u.mu.RUnlock()
	user, ok := u.users[uuid]
	if !ok || !user.Enabled {
		return 0, false
	}
	return user.SlotPort, true
}

func (u *vlessUsers) Add(name string, slotPort int, store *Store) (VLESSUser, error) {
	u.mu.Lock()
	defer u.mu.Unlock()

	uuidBytes := make([]byte, 16)
	if _, err := rand.Read(uuidBytes); err != nil {
		return VLESSUser{}, err
	}
	// UUID v4 RFC 4122
	uuidBytes[6] = (uuidBytes[6] & 0x0f) | 0x40
	uuidBytes[8] = (uuidBytes[8] & 0x3f) | 0x80

	var uuidKey [16]byte
	copy(uuidKey[:], uuidBytes)
	uuidStr := formatUUID(uuidKey)

	user := VLESSUser{
		UUID:      uuidStr,
		Name:      name,
		SlotPort:  slotPort,
		Enabled:   true,
		CreatedAt: time.Now().UTC(),
	}

	u.users[uuidKey] = &user
	u.slots[slotPort] = uuidKey

	if store != nil {
		store.PutUser(UserRecord{
			UUID:      user.UUID,
			Name:      user.Name,
			SlotPort:  user.SlotPort,
			Enabled:   user.Enabled,
			CreatedAt: user.CreatedAt,
		})
		_ = store.Save()
	}

	return user, nil
}

func (u *vlessUsers) Remove(uuidStr string, store *Store) bool {
	uuidKey, err := parseCanonicalUUID(uuidStr)
	if err != nil {
		return false
	}

	u.mu.Lock()
	defer u.mu.Unlock()

	user, exists := u.users[uuidKey]
	if !exists {
		return false
	}

	delete(u.users, uuidKey)
	delete(u.slots, user.SlotPort)

	if store != nil {
		store.DeleteUser(uuidStr)
		_ = store.Save()
	}
	return true
}

func (u *vlessUsers) List() []VLESSUser {
	u.mu.RLock()
	defer u.mu.RUnlock()

	res := make([]VLESSUser, 0, len(u.users))
	for _, usr := range u.users {
		res = append(res, *usr)
	}
	return res
}

func (u *vlessUsers) LoadFromStore(records []UserRecord) {
	u.mu.Lock()
	defer u.mu.Unlock()

	for _, rec := range records {
		uuidKey, err := parseCanonicalUUID(rec.UUID)
		if err != nil {
			continue
		}
		user := VLESSUser{
			UUID:      rec.UUID,
			Name:      rec.Name,
			SlotPort:  rec.SlotPort,
			Enabled:   rec.Enabled,
			CreatedAt: rec.CreatedAt,
		}
		u.users[uuidKey] = &user
		u.slots[rec.SlotPort] = uuidKey
	}
}

func (u *vlessUsers) GenerateURI(user VLESSUser, config Config, reqHost string) string {
	security := "tls"
	if config.TLSMode == "none" {
		security = "none"
	}

	host := config.TLSDomain
	if host == "" {
		if reqHost != "" {
			h, _, err := net.SplitHostPort(reqHost)
			if err == nil && h != "" {
				host = h
			} else {
				host = reqHost
			}
		}
		if host == "" {
			host = "127.0.0.1"
		}
	}
	_, portStr, _ := net.SplitHostPort(config.VLESSListen)
	port, _ := strconv.Atoi(portStr)

	params := url.Values{}
	params.Set("encryption", "none")
	params.Set("security", security)
	params.Set("type", "tcp")
	params.Set("headerType", "none")
	if config.TLSMode == "acme" && config.TLSDomain != "" {
		params.Set("sni", config.TLSDomain)
	}

	return fmt.Sprintf("vless://%s@%s:%d?%s#%s",
		user.UUID,
		host,
		port,
		params.Encode(),
		url.QueryEscape(user.Name),
	)
}

func parseCanonicalUUID(text string) ([16]byte, error) {
	var uuid [16]byte
	clean := strings.ReplaceAll(strings.TrimSpace(text), "-", "")
	if len(clean) != 32 {
		return uuid, fmt.Errorf("invalid UUID length: %q", text)
	}
	b, err := hex.DecodeString(clean)
	if err != nil {
		return uuid, err
	}
	copy(uuid[:], b)
	return uuid, nil
}

func formatUUID(uuid [16]byte) string {
	hexStr := hex.EncodeToString(uuid[:])
	return fmt.Sprintf("%s-%s-%s-%s-%s",
		hexStr[0:8], hexStr[8:12], hexStr[12:16], hexStr[16:20], hexStr[20:32])
}
