package main

import (
	"bufio"
	"bytes"
	"encoding/binary"
	"fmt"
	"io"
	"net"
	"strconv"
	"time"
)

const (
	socksVersion        = 0x05
	socksNoAuth         = 0x00
	socksCommandConnect = 0x01
	socksReplySuccess   = 0x00
	socksAtypIPv4       = 0x01
	socksAtypDomain     = 0x03
	socksAtypIPv6       = 0x04
)

// dialUpstreamSOCKS opens the private Tailscale connection to a selected Spot,
// then asks that Spot's no-auth SOCKS5 service to connect to the VLESS target.
func dialUpstreamSOCKS(decision routeDecision, destination vlessRequest, timeout time.Duration) (net.Conn, *bufio.Reader, error) {
	if decision.ipv4 == "" || decision.upstreamPort <= 0 {
		return nil, nil, fmt.Errorf("incomplete upstream route")
	}
	address := net.JoinHostPort(decision.ipv4, strconv.Itoa(decision.upstreamPort))
	connection, err := net.DialTimeout("tcp", address, timeout)
	if err != nil {
		return nil, nil, err
	}
	failed := true
	defer func() {
		if failed {
			_ = connection.Close()
		}
	}()

	_ = connection.SetDeadline(time.Now().Add(15 * time.Second))
	reader := bufio.NewReader(connection)
	if _, err := connection.Write([]byte{socksVersion, 0x01, socksNoAuth}); err != nil {
		return nil, nil, err
	}
	methodReply := make([]byte, 2)
	if _, err := io.ReadFull(reader, methodReply); err != nil {
		return nil, nil, err
	}
	if methodReply[0] != socksVersion || methodReply[1] != socksNoAuth {
		return nil, nil, fmt.Errorf("upstream SOCKS5 refused no-auth method: %v", methodReply)
	}

	request, err := encodeUpstreamSOCKSConnect(destination)
	if err != nil {
		return nil, nil, err
	}
	if _, err := connection.Write(request); err != nil {
		return nil, nil, err
	}
	_, replyCode, err := readSOCKS5Frame(reader)
	if err != nil {
		return nil, nil, err
	}
	if replyCode != socksReplySuccess {
		return nil, nil, fmt.Errorf("upstream SOCKS5 CONNECT failed with reply %d", replyCode)
	}
	failed = false
	return connection, reader, nil
}

func encodeUpstreamSOCKSConnect(destination vlessRequest) ([]byte, error) {
	if destination.Command != vlessTCP || destination.Port == 0 {
		return nil, fmt.Errorf("unsupported destination")
	}
	frame := []byte{socksVersion, socksCommandConnect, 0x00}
	switch destination.AddressType {
	case vlessIPv4:
		address := net.ParseIP(destination.Host).To4()
		if address == nil {
			return nil, fmt.Errorf("invalid IPv4 destination %q", destination.Host)
		}
		frame = append(frame, socksAtypIPv4)
		frame = append(frame, address...)
	case vlessDomain:
		if len(destination.Host) == 0 || len(destination.Host) > 255 {
			return nil, fmt.Errorf("invalid domain destination")
		}
		frame = append(frame, socksAtypDomain, byte(len(destination.Host)))
		frame = append(frame, destination.Host...)
	case vlessIPv6:
		address := net.ParseIP(destination.Host)
		if address == nil || address.To4() != nil || address.To16() == nil {
			return nil, fmt.Errorf("invalid IPv6 destination %q", destination.Host)
		}
		frame = append(frame, socksAtypIPv6)
		frame = append(frame, address.To16()...)
	default:
		return nil, fmt.Errorf("unsupported destination address type %d", destination.AddressType)
	}
	port := make([]byte, 2)
	binary.BigEndian.PutUint16(port, destination.Port)
	return append(frame, port...), nil
}

// readSOCKS5Frame reads a complete RFC 1928 request or reply and returns its
// raw bytes together with the second byte (command or reply code).
func readSOCKS5Frame(reader io.Reader) ([]byte, byte, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(reader, header); err != nil {
		return nil, 0, err
	}
	if header[0] != socksVersion {
		return nil, 0, fmt.Errorf("unexpected SOCKS version %d", header[0])
	}
	frame := bytes.NewBuffer(header[:4:4])
	addressLength := 0
	switch header[3] {
	case socksAtypIPv4:
		addressLength = net.IPv4len
	case socksAtypIPv6:
		addressLength = net.IPv6len
	case socksAtypDomain:
		length := make([]byte, 1)
		if _, err := io.ReadFull(reader, length); err != nil {
			return nil, 0, err
		}
		if length[0] == 0 {
			return nil, 0, fmt.Errorf("empty SOCKS5 domain")
		}
		frame.Write(length)
		addressLength = int(length[0])
	default:
		return nil, 0, fmt.Errorf("unsupported SOCKS5 address type %d", header[3])
	}
	rest := make([]byte, addressLength+2)
	if _, err := io.ReadFull(reader, rest); err != nil {
		return nil, 0, err
	}
	frame.Write(rest)
	return frame.Bytes(), header[1], nil
}
