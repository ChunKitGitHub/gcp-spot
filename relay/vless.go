package main

import (
	"bufio"
	"crypto/tls"
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"log"
	"net"
	"sync"
	"time"
)

const (
	vlessVersion = 0x00
	vlessTCP     = 0x01
	vlessIPv4    = 0x01
	vlessDomain  = 0x02
	vlessIPv6    = 0x03

	// A cache miss makes autocert obtain a certificate during the TLS
	// handshake. That can take longer than an ordinary VLESS request header.
	vlessTLSHandshakeTimeout     = 2 * time.Minute
	vlessRequestHandshakeTimeout = 15 * time.Second
)

// vlessRequest is the TCP destination encoded by a VLESS client.
type vlessRequest struct {
	UUID        [16]byte
	Command     byte
	Host        string
	Port        uint16
	AddressType byte
}

// vlessServer accepts VLESS sessions from a TLS listener supplied by main.
type vlessServer struct {
	config Config
	conns  *connRegistry
	users  *vlessUsers
	route  func(int) (routeDecision, bool)

	mu       sync.Mutex
	listener net.Listener
}

func newVLESSServer(config Config, conns *connRegistry, users *vlessUsers, route func(int) (routeDecision, bool)) *vlessServer {
	return &vlessServer{config: config, conns: conns, users: users, route: route}
}

// Serve blocks until the supplied listener is closed. It owns no TLS policy;
// callers must supply a TLS-wrapped listener in production and tests.
func (server *vlessServer) Serve(listener net.Listener) error {
	server.mu.Lock()
	if server.listener != nil {
		server.mu.Unlock()
		return fmt.Errorf("VLESS server is already serving")
	}
	server.listener = listener
	server.mu.Unlock()

	for {
		connection, err := listener.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go server.handleConnection(connection)
	}
}

func (server *vlessServer) Shutdown() error {
	server.mu.Lock()
	listener := server.listener
	server.listener = nil
	server.mu.Unlock()
	if listener == nil {
		return nil
	}
	if err := listener.Close(); errors.Is(err, net.ErrClosed) {
		return nil
	} else {
		return err
	}
}

func (server *vlessServer) handleConnection(client net.Conn) {
	defer client.Close()
	_ = client.SetDeadline(time.Now().Add(vlessTLSHandshakeTimeout))
	if tlsClient, ok := client.(*tls.Conn); ok {
		if err := tlsClient.Handshake(); err != nil {
			// A failed TLS handshake commonly contains the only actionable ACME
			// error. Do not log VLESS request bytes, UUIDs, or destinations.
			log.Printf("TLS handshake failed from %v: %v", client.RemoteAddr(), err)
			return
		}
	}
	_ = client.SetDeadline(time.Now().Add(vlessRequestHandshakeTimeout))
	clientReader := bufio.NewReader(client)

	destination, err := readVLESSRequest(clientReader)
	if err != nil {
		log.Printf("VLESS decode request failed from %v: %v", client.RemoteAddr(), err)
		return
	}
	slotPort, ok := server.users.Slot(destination.UUID)
	if !ok {
		log.Printf("VLESS rejected: unknown user UUID %s from %v", formatUUID(destination.UUID), client.RemoteAddr())
		return
	}
	decision, ok := server.route(slotPort)
	if !ok {
		log.Printf("VLESS rejected: no available spot route for slot %d (user %s)", slotPort, formatUUID(destination.UUID))
		return
	}
	connectionID := server.conns.add(slotPort, decision.instance)
	defer server.conns.remove(connectionID)

	log.Printf("VLESS tunnel open: %v (user slot %d) -> Spot %s (%s:%d) -> %s:%d",
		client.RemoteAddr(), slotPort, decision.instance, decision.ipv4, decision.upstreamPort,
		destination.Host, destination.Port)

	upstream, upstreamReader, err := dialUpstreamSOCKS(decision, destination, server.config.DialTimeout)
	if err != nil {
		log.Printf("VLESS dial upstream Spot %s (%s:%d) failed: %v",
			decision.instance, decision.ipv4, decision.upstreamPort, err)
		return
	}
	defer upstream.Close()
	if _, err := client.Write([]byte{vlessVersion, 0x00}); err != nil {
		log.Printf("VLESS write response to client %v failed: %v", client.RemoteAddr(), err)
		return
	}
	_ = client.SetDeadline(time.Time{})
	_ = upstream.SetDeadline(time.Time{})
	copyBidirectional(client, clientReader, upstream, upstreamReader)
	log.Printf("VLESS tunnel closed: %v -> %s:%d", client.RemoteAddr(), destination.Host, destination.Port)
}

// readVLESSRequest reads exactly one VLESS request and rejects all unsupported
// commands before any upstream connection is attempted.
func readVLESSRequest(reader io.Reader) (vlessRequest, error) {
	version := make([]byte, 1)
	if _, err := io.ReadFull(reader, version); err != nil {
		return vlessRequest{}, err
	}
	if version[0] != vlessVersion {
		return vlessRequest{}, fmt.Errorf("unsupported VLESS version %d", version[0])
	}
	var uuid [16]byte
	if _, err := io.ReadFull(reader, uuid[:]); err != nil {
		return vlessRequest{}, err
	}
	addonLength := make([]byte, 1)
	if _, err := io.ReadFull(reader, addonLength); err != nil {
		return vlessRequest{}, err
	}
	if addonLength[0] > 0 {
		addons := make([]byte, addonLength[0])
		if _, err := io.ReadFull(reader, addons); err != nil {
			return vlessRequest{}, err
		}
		// Gracefully consume addons (such as mux or flow metadata)
	}

	header := make([]byte, 4) // command, port high, port low, address type
	if _, err := io.ReadFull(reader, header); err != nil {
		return vlessRequest{}, err
	}
	request := vlessRequest{
		UUID:        uuid,
		Command:     header[0],
		Port:        binary.BigEndian.Uint16(header[1:3]),
		AddressType: header[3],
	}
	if request.Command != vlessTCP {
		return vlessRequest{}, fmt.Errorf("unsupported VLESS command %d", request.Command)
	}
	if request.Port == 0 {
		return vlessRequest{}, fmt.Errorf("zero VLESS destination port")
	}
	switch request.AddressType {
	case vlessIPv4:
		address := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(reader, address); err != nil {
			return vlessRequest{}, err
		}
		request.Host = net.IP(address).String()
	case vlessDomain:
		length := make([]byte, 1)
		if _, err := io.ReadFull(reader, length); err != nil {
			return vlessRequest{}, err
		}
		if length[0] == 0 {
			return vlessRequest{}, fmt.Errorf("empty VLESS domain")
		}
		domain := make([]byte, length[0])
		if _, err := io.ReadFull(reader, domain); err != nil {
			return vlessRequest{}, err
		}
		request.Host = string(domain)
	case vlessIPv6:
		address := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(reader, address); err != nil {
			return vlessRequest{}, err
		}
		request.Host = net.IP(address).String()
	default:
		return vlessRequest{}, fmt.Errorf("unsupported VLESS address type %d", request.AddressType)
	}
	return request, nil
}

func copyBidirectional(client net.Conn, clientReader io.Reader, upstream net.Conn, upstreamReader io.Reader) {
	var group sync.WaitGroup
	group.Add(2)
	go func() {
		defer group.Done()
		_, _ = io.Copy(upstream, clientReader)
		closeWrite(upstream)
	}()
	go func() {
		defer group.Done()
		_, _ = io.Copy(client, upstreamReader)
		closeWrite(client)
	}()
	group.Wait()
}

func closeWrite(connection net.Conn) {
	if halfCloser, ok := connection.(interface{ CloseWrite() error }); ok {
		_ = halfCloser.CloseWrite()
		return
	}
	_ = connection.Close()
}
