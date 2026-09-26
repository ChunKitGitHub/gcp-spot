package main

import (
	"encoding/binary"
	"errors"
	"fmt"
	"io"
	"net"
	"strconv"
	"sync"
	"time"
)

const (
	socksVersion = 0x05

	// Auth methods
	socksAuthNone = 0x00
	socksAuthNoAcceptable = 0xFF

	// Commands
	socksCmdConnect = 0x01

	// Address types
	socksAtypIPv4   = 0x01
	socksAtypDomain = 0x03
	socksAtypIPv6   = 0x04

	// Reply status
	socksReplySuccess            = 0x00
	socksReplyGeneralFailure     = 0x01
	socksReplyConnNotAllowed     = 0x02
	socksReplyNetworkUnreachable = 0x03
	socksReplyHostUnreachable    = 0x04
	socksReplyConnRefused        = 0x05
	socksReplyTTLExpired         = 0x06
	socksReplyCmdNotSupported    = 0x07
	socksReplyAtypNotSupported   = 0x08
)

type SOCKS5Server struct {
	addr        string
	dialTimeout time.Duration
	listener    net.Listener
	mu          sync.Mutex
}

func NewSOCKS5Server(addr string, dialTimeout time.Duration) *SOCKS5Server {
	return &SOCKS5Server{
		addr:        addr,
		dialTimeout: dialTimeout,
	}
}

func (s *SOCKS5Server) ListenAndServe() error {
	l, err := net.Listen("tcp", s.addr)
	if err != nil {
		return err
	}
	return s.Serve(l)
}

func (s *SOCKS5Server) Serve(l net.Listener) error {
	s.mu.Lock()
	s.listener = l
	s.mu.Unlock()

	for {
		conn, err := l.Accept()
		if err != nil {
			if errors.Is(err, net.ErrClosed) {
				return nil
			}
			return err
		}
		go s.handleConn(conn)
	}
}

func (s *SOCKS5Server) Close() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.listener != nil {
		return s.listener.Close()
	}
	return nil
}

func (s *SOCKS5Server) handleConn(conn net.Conn) {
	defer conn.Close()
	_ = conn.SetDeadline(time.Now().Add(15 * time.Second))

	// 1. 协商阶段
	if err := s.authHandshake(conn); err != nil {
		return
	}

	// 2. 请求阶段
	targetAddr, err := s.readRequest(conn)
	if err != nil {
		return
	}

	// 3. 拨号目标
	destConn, err := net.DialTimeout("tcp", targetAddr, s.dialTimeout)
	if err != nil {
		s.sendReply(conn, socksReplyHostUnreachable, nil)
		return
	}
	defer destConn.Close()

	// 4. 发送成功应答
	localAddr := destConn.LocalAddr().(*net.TCPAddr)
	if err := s.sendReply(conn, socksReplySuccess, localAddr); err != nil {
		return
	}

	// 5. 转发流量
	_ = conn.SetDeadline(time.Time{})
	_ = destConn.SetDeadline(time.Time{})
	pipe(conn, destConn)
}

func (s *SOCKS5Server) authHandshake(conn net.Conn) error {
	buf := make([]byte, 2)
	if _, err := io.ReadFull(conn, buf); err != nil {
		return err
	}
	if buf[0] != socksVersion {
		return fmt.Errorf("unsupported socks version: %d", buf[0])
	}
	nmethods := int(buf[1])
	methods := make([]byte, nmethods)
	if _, err := io.ReadFull(conn, methods); err != nil {
		return err
	}

	hasNone := false
	for _, m := range methods {
		if m == socksAuthNone {
			hasNone = true
			break
		}
	}
	if !hasNone {
		_, _ = conn.Write([]byte{socksVersion, socksAuthNoAcceptable})
		return fmt.Errorf("no acceptable auth method")
	}

	_, err := conn.Write([]byte{socksVersion, socksAuthNone})
	return err
}

func (s *SOCKS5Server) readRequest(conn net.Conn) (string, error) {
	header := make([]byte, 4)
	if _, err := io.ReadFull(conn, header); err != nil {
		return "", err
	}
	if header[0] != socksVersion {
		return "", fmt.Errorf("unsupported socks version: %d", header[0])
	}
	cmd := header[1]
	atyp := header[3]

	if cmd != socksCmdConnect {
		s.sendReply(conn, socksReplyCmdNotSupported, nil)
		return "", fmt.Errorf("unsupported command: %d", cmd)
	}

	var host string
	switch atyp {
	case socksAtypIPv4:
		ip := make([]byte, net.IPv4len)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return "", err
		}
		host = net.IP(ip).String()
	case socksAtypDomain:
		lenBuf := make([]byte, 1)
		if _, err := io.ReadFull(conn, lenBuf); err != nil {
			return "", err
		}
		domainLen := int(lenBuf[0])
		domainBuf := make([]byte, domainLen)
		if _, err := io.ReadFull(conn, domainBuf); err != nil {
			return "", err
		}
		host = string(domainBuf)
	case socksAtypIPv6:
		ip := make([]byte, net.IPv6len)
		if _, err := io.ReadFull(conn, ip); err != nil {
			return "", err
		}
		host = net.IP(ip).String()
	default:
		s.sendReply(conn, socksReplyAtypNotSupported, nil)
		return "", fmt.Errorf("unsupported atyp: %d", atyp)
	}

	portBuf := make([]byte, 2)
	if _, err := io.ReadFull(conn, portBuf); err != nil {
		return "", err
	}
	port := binary.BigEndian.Uint16(portBuf)

	return net.JoinHostPort(host, strconv.Itoa(int(port))), nil
}

func (s *SOCKS5Server) sendReply(conn net.Conn, replyCode byte, bindAddr *net.TCPAddr) error {
	reply := []byte{socksVersion, replyCode, 0x00}
	if bindAddr == nil || bindAddr.IP.To4() != nil {
		reply = append(reply, socksAtypIPv4)
		ip := net.IPv4zero
		if bindAddr != nil {
			ip = bindAddr.IP.To4()
		}
		reply = append(reply, ip...)
	} else {
		reply = append(reply, socksAtypIPv6)
		reply = append(reply, bindAddr.IP.To16()...)
	}

	port := uint16(0)
	if bindAddr != nil {
		port = uint16(bindAddr.Port)
	}
	portBytes := make([]byte, 2)
	binary.BigEndian.PutUint16(portBytes, port)
	reply = append(reply, portBytes...)

	_, err := conn.Write(reply)
	return err
}

func pipe(c1, c2 net.Conn) {
	var wg sync.WaitGroup
	wg.Add(2)

	go func() {
		defer wg.Done()
		_, _ = io.Copy(c2, c1)
		closeWrite(c2)
	}()

	go func() {
		defer wg.Done()
		_, _ = io.Copy(c1, c2)
		closeWrite(c1)
	}()

	wg.Wait()
}

func closeWrite(conn net.Conn) {
	if tcp, ok := conn.(*net.TCPConn); ok {
		_ = tcp.CloseWrite()
	} else {
		_ = conn.Close()
	}
}
