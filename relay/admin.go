package main

import (
	"crypto/tls"
	"embed"
	"encoding/json"
	"fmt"
	"io"
	"io/fs"
	"log"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"
)

//go:embed web/*
var webFS embed.FS

type adminServer struct {
	config    Config
	spots     *spotRegistry
	pool      *pool
	users     *vlessUsers
	conns     *connRegistry
	store     *Store
	http      *http.Server
	tlsConfig *tls.Config
}

func newAdminServer(config Config, spots *spotRegistry, pool *pool, users *vlessUsers, conns *connRegistry, store *Store, tlsConfig *tls.Config) *adminServer {
	s := &adminServer{
		config:    config,
		spots:     spots,
		pool:      pool,
		users:     users,
		conns:     conns,
		store:     store,
		tlsConfig: tlsConfig,
	}

	mux := http.NewServeMux()

	// 心跳接口 (AGENT_TOKEN 验证)
	mux.HandleFunc("POST /api/heartbeat", s.handleHeartbeat)

	// 管理接口 (ADMIN_TOKEN 验证)
	mux.HandleFunc("GET /api/status", s.requireAdmin(s.handleStatus))
	mux.HandleFunc("DELETE /api/spots/{name}", s.requireAdmin(s.handleDeleteSpot))
	mux.HandleFunc("DELETE /api/slots/{port}", s.requireAdmin(s.handleDeleteSlot))
	mux.HandleFunc("GET /api/users", s.requireAdmin(s.handleListUsers))
	mux.HandleFunc("POST /api/users", s.requireAdmin(s.handleCreateUser))
	mux.HandleFunc("DELETE /api/users/{uuid}", s.requireAdmin(s.handleDeleteUser))
	mux.HandleFunc("POST /api/spots/{name}/test", s.requireAdmin(s.handleTestSpot))

	// Spot 二进制与安装脚本分发
	mux.HandleFunc("GET /download/spot-agent", s.handleDownloadAgent)
	mux.HandleFunc("GET /bootstrap.sh", s.handleBootstrapScript)

	// Web UI 静态文件
	subFS, _ := fs.Sub(webFS, "web")
	fileServer := http.FileServer(http.FS(subFS))
	mux.Handle("GET /web/", http.StripPrefix("/web", fileServer))
	mux.HandleFunc("GET /", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/" {
			http.Redirect(w, r, "/web/", http.StatusFound)
			return
		}
		fileServer.ServeHTTP(w, r)
	})

	s.http = &http.Server{
		Addr:              config.AdminListen,
		Handler:           mux,
		ReadHeaderTimeout: 10 * time.Second,
	}
	return s
}

func (s *adminServer) ListenAndServe() error {
	l, err := net.Listen("tcp", s.config.AdminListen)
	if err != nil {
		return err
	}
	defer l.Close()

	dual := &dualListener{
		Listener:  l,
		tlsConfig: s.tlsConfig,
	}
	return s.http.Serve(dual)
}

type dualListener struct {
	net.Listener
	tlsConfig *tls.Config
}

func (l *dualListener) Accept() (net.Conn, error) {
	conn, err := l.Listener.Accept()
	if err != nil {
		return nil, err
	}
	if l.tlsConfig == nil {
		return conn, nil
	}

	_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 1)
	n, err := conn.Read(buf)
	_ = conn.SetReadDeadline(time.Time{})
	if err != nil || n == 0 {
		_ = conn.Close()
		return nil, err
	}

	prefixed := &prefixedConn{Conn: conn, prefix: buf}
	if buf[0] == 0x16 { // TLS Handshake record type
		return tls.Server(prefixed, l.tlsConfig), nil
	}
	return prefixed, nil
}

type prefixedConn struct {
	net.Conn
	prefix []byte
}

func (p *prefixedConn) Read(b []byte) (int, error) {
	if len(p.prefix) > 0 {
		n := copy(b, p.prefix)
		p.prefix = p.prefix[n:]
		return n, nil
	}
	return p.Conn.Read(b)
}

func (s *adminServer) requireAdmin(next http.HandlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		token := strings.TrimPrefix(auth, "Bearer ")
		if token != s.config.AdminToken {
			http.Error(w, "Unauthorized", http.StatusUnauthorized)
			return
		}
		next(w, r)
	}
}

func (s *adminServer) handleHeartbeat(w http.ResponseWriter, r *http.Request) {
	auth := r.Header.Get("Authorization")
	token := strings.TrimPrefix(auth, "Bearer ")
	if token != s.config.AgentToken {
		http.Error(w, "Unauthorized", http.StatusUnauthorized)
		return
	}

	var payload SpotInfo
	if err := json.NewDecoder(r.Body).Decode(&payload); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}
	if payload.InstanceName == "" || payload.InternalIP == "" {
		http.Error(w, "Missing instance_name or internal_ip", http.StatusBadRequest)
		return
	}

	isNew := s.spots.Heartbeat(payload, time.Now().UTC())
	if isNew {
		log.Printf("new spot registered: %s (internal: %s, exit: %s)",
			payload.InstanceName, payload.InternalIP, payload.PublicIP)
		// 新节点注册立即触发一次同步
		go s.pool.syncOnce()
	}

	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(map[string]string{"status": "ok"})
}

type statusResponse struct {
	Spots       []SpotInfo `json:"spots"`
	Slots       []target   `json:"slots"`
	ActiveConns int        `json:"active_conns"`
}

func (s *adminServer) handleStatus(w http.ResponseWriter, r *http.Request) {
	now := time.Now().UTC()
	resp := statusResponse{
		Spots:       s.spots.AllSpots(s.config.HeartbeatTimeout, now),
		Slots:       s.pool.statusTargets(),
		ActiveConns: s.conns.totalActive(),
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(resp)
}

type userView struct {
	UUID      string    `json:"uuid"`
	Name      string    `json:"name"`
	SlotPort  int       `json:"slot_port"`
	Enabled   bool      `json:"enabled"`
	CreatedAt time.Time `json:"created_at"`
	URI       string    `json:"uri"`
}

func (s *adminServer) handleListUsers(w http.ResponseWriter, r *http.Request) {
	users := s.users.List()
	var views []userView
	for _, u := range users {
		views = append(views, userView{
			UUID:      u.UUID,
			Name:      u.Name,
			SlotPort:  u.SlotPort,
			Enabled:   u.Enabled,
			CreatedAt: u.CreatedAt,
			URI:       s.users.GenerateURI(u, s.config, r.Host),
		})
	}
	w.Header().Set("Content-Type", "application/json")
	_ = json.NewEncoder(w).Encode(views)
}

type createUserReq struct {
	Name     string `json:"name"`
	SlotPort *int   `json:"slot_port"`
}

func (s *adminServer) handleCreateUser(w http.ResponseWriter, r *http.Request) {
	var req createUserReq
	if err := json.NewDecoder(r.Body).Decode(&req); err != nil {
		http.Error(w, "Invalid body", http.StatusBadRequest)
		return
	}
	if strings.TrimSpace(req.Name) == "" {
		http.Error(w, "Name is required", http.StatusBadRequest)
		return
	}

	slotPort := 0
	if req.SlotPort != nil && *req.SlotPort > 0 {
		slotPort = *req.SlotPort
	} else {
		// 寻找最小可用槽位
		slots := s.store.Slots()
		usedSlots := make(map[int]bool)
		for _, u := range s.users.List() {
			usedSlots[u.SlotPort] = true
		}
		for _, sl := range slots {
			if !usedSlots[sl.Port] {
				slotPort = sl.Port
				break
			}
		}
		if slotPort == 0 && len(slots) > 0 {
			slotPort = slots[0].Port
		}
		if slotPort == 0 {
			slotPort = s.config.PortRangeStart
		}
	}

	user, err := s.users.Add(req.Name, slotPort, s.store)
	if err != nil {
		http.Error(w, err.Error(), http.StatusInternalServerError)
		return
	}

	view := userView{
		UUID:      user.UUID,
		Name:      user.Name,
		SlotPort:  user.SlotPort,
		Enabled:   user.Enabled,
		CreatedAt: user.CreatedAt,
		URI:       s.users.GenerateURI(user, s.config, r.Host),
	}
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusCreated)
	_ = json.NewEncoder(w).Encode(view)
}

func (s *adminServer) handleDeleteUser(w http.ResponseWriter, r *http.Request) {
	uuid := r.PathValue("uuid")
	if !s.users.Remove(uuid, s.store) {
		http.Error(w, "User not found", http.StatusNotFound)
		return
	}
	w.WriteHeader(http.StatusNoContent)
}

func (s *adminServer) handleDeleteSpot(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	if name == "" {
		http.Error(w, "Instance name is required", http.StatusBadRequest)
		return
	}
	s.spots.Remove(name)
	s.store.ReclaimSlotByInstance(name)
	_ = s.store.Save()
	s.pool.removeSpot(name)
	w.WriteHeader(http.StatusNoContent)
}

func (s *adminServer) handleDeleteSlot(w http.ResponseWriter, r *http.Request) {
	portStr := r.PathValue("port")
	port, err := strconv.Atoi(portStr)
	if err != nil {
		http.Error(w, "Invalid port", http.StatusBadRequest)
		return
	}
	s.store.ReclaimSlot(port)
	_ = s.store.Save()
	s.pool.removeSlot(port)
	w.WriteHeader(http.StatusNoContent)
}

type testSpotResponse struct {
	Success bool   `json:"success"`
	Latency string `json:"latency,omitempty"`
	Error   string `json:"error,omitempty"`
}

func (s *adminServer) handleTestSpot(w http.ResponseWriter, r *http.Request) {
	name := r.PathValue("name")
	spots := s.spots.AllSpots(s.config.HeartbeatTimeout, time.Now().UTC())
	var targetSpot *SpotInfo
	for _, sp := range spots {
		if sp.InstanceName == name {
			targetSpot = &sp
			break
		}
	}

	w.Header().Set("Content-Type", "application/json")
	if targetSpot == nil {
		_ = json.NewEncoder(w).Encode(testSpotResponse{Success: false, Error: "Spot not found"})
		return
	}

	start := time.Now()
	addr := fmt.Sprintf("%s:%d", targetSpot.InternalIP, targetSpot.SOCKSPort)
	conn, err := net.DialTimeout("tcp", addr, 3*time.Second)
	if err != nil {
		_ = json.NewEncoder(w).Encode(testSpotResponse{
			Success: false,
			Error:   fmt.Sprintf("TCP dial to %s failed: %v (check GCP firewall tcp:%d)", addr, err, targetSpot.SOCKSPort),
		})
		return
	}
	defer conn.Close()

	// SOCKS5 握手
	_ = conn.SetDeadline(time.Now().Add(3 * time.Second))
	if _, err := conn.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		_ = json.NewEncoder(w).Encode(testSpotResponse{Success: false, Error: fmt.Sprintf("SOCKS5 write failed: %v", err)})
		return
	}
	resp := make([]byte, 2)
	if _, err := io.ReadFull(conn, resp); err != nil || resp[1] != 0x00 {
		_ = json.NewEncoder(w).Encode(testSpotResponse{Success: false, Error: "SOCKS5 handshake rejected"})
		return
	}

	latency := time.Since(start).Round(time.Millisecond).String()
	_ = json.NewEncoder(w).Encode(testSpotResponse{Success: true, Latency: latency})
}

func (s *adminServer) handleDownloadAgent(w http.ResponseWriter, r *http.Request) {
	arch := r.URL.Query().Get("arch")
	if arch == "" {
		arch = "amd64"
	}
	binName := fmt.Sprintf("spot-agent-linux-%s", arch)
	binPath := filepath.Join(s.config.AgentBinaryDir, binName)

	f, err := os.Open(binPath)
	if err != nil {
		// 备用查找路径
		altPath := filepath.Join("../spot/dist", binName)
		f, err = os.Open(altPath)
		if err != nil {
			http.Error(w, fmt.Sprintf("Binary not found: %s", binName), http.StatusNotFound)
			return
		}
	}
	defer f.Close()

	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", fmt.Sprintf("attachment; filename=%s", binName))
	_, _ = io.Copy(w, f)
}

func (s *adminServer) handleBootstrapScript(w http.ResponseWriter, r *http.Request) {
	host := r.Host
	token := s.config.AgentToken

	script := fmt.Sprintf(`#!/usr/bin/env bash
set -Eeuo pipefail

ARCH="$(uname -m)"
case "${ARCH}" in
    x86_64)  ARCH_NAME="amd64" ;;
    aarch64|arm64) ARCH_NAME="arm64" ;;
    *) echo "Unsupported arch: ${ARCH}"; exit 1 ;;
esac

echo "Downloading spot-agent..."
curl -fsSL "http://%s/download/spot-agent?arch=${ARCH_NAME}" -o /usr/local/bin/spot-agent
chmod +x /usr/local/bin/spot-agent

cat > /etc/default/spot-agent << 'EOF'
RELAY_URL=http://%s
AGENT_TOKEN=%s
SOCKS_LISTEN=0.0.0.0:1080
EOF

cat > /etc/systemd/system/spot-agent.service << 'EOF'
[Unit]
Description=Spot Agent (Built-in SOCKS5 + Heartbeat)
After=network-online.target
Wants=network-online.target

[Service]
Type=simple
EnvironmentFile=/etc/default/spot-agent
ExecStart=/usr/local/bin/spot-agent
Restart=always
RestartSec=3
LimitNOFILE=65535

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable --now spot-agent
echo "spot-agent started successfully."
`, host, host, token)

	w.Header().Set("Content-Type", "text/x-shellscript")
	_, _ = w.Write([]byte(script))
}
