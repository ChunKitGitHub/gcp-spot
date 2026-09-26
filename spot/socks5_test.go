package main

import (
	"bytes"
	"io"
	"net"
	"testing"
	"time"
)

func TestSOCKS5Echo(t *testing.T) {
	// 1. 启动一个临时 echo 服务器
	echoListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen echo server: %v", err)
	}
	defer echoListener.Close()

	go func() {
		for {
			conn, err := echoListener.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				defer c.Close()
				_, _ = io.Copy(c, c)
			}(conn)
		}
	}()

	// 2. 启动 SOCKS5 服务器
	socksServer := NewSOCKS5Server("127.0.0.1:0", 5*time.Second)
	socksListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen socks server: %v", err)
	}
	defer socksListener.Close()

	go func() {
		_ = socksServer.Serve(socksListener)
	}()

	// 3. 客户端连接 SOCKS5 服务器并请求连接 echo 服务器
	client, err := net.Dial("tcp", socksListener.Addr().String())
	if err != nil {
		t.Fatalf("dial socks: %v", err)
	}
	defer client.Close()

	// 认证握手 (version 5, 1 method: none)
	if _, err := client.Write([]byte{0x05, 0x01, 0x00}); err != nil {
		t.Fatalf("write auth: %v", err)
	}
	authResp := make([]byte, 2)
	if _, err := io.ReadFull(client, authResp); err != nil {
		t.Fatalf("read auth resp: %v", err)
	}
	if authResp[0] != 0x05 || authResp[1] != 0x00 {
		t.Fatalf("auth resp mismatch: %v", authResp)
	}

	// 解析 echo 服务器端口和 IP
	echoTCP := echoListener.Addr().(*net.TCPAddr)
	req := []byte{0x05, 0x01, 0x00, 0x01} // cmd: connect, atyp: ipv4
	req = append(req, echoTCP.IP.To4()...)
	req = append(req, byte(echoTCP.Port>>8), byte(echoTCP.Port&0xFF))

	if _, err := client.Write(req); err != nil {
		t.Fatalf("write connect req: %v", err)
	}

	connectResp := make([]byte, 10)
	if _, err := io.ReadFull(client, connectResp); err != nil {
		t.Fatalf("read connect resp: %v", err)
	}
	if connectResp[1] != 0x00 {
		t.Fatalf("connect failed with code: %d", connectResp[1])
	}

	// 4. 数据往返测试
	testData := []byte("hello socks5 echo")
	if _, err := client.Write(testData); err != nil {
		t.Fatalf("write test data: %v", err)
	}
	recvData := make([]byte, len(testData))
	if _, err := io.ReadFull(client, recvData); err != nil {
		t.Fatalf("read test data: %v", err)
	}
	if !bytes.Equal(testData, recvData) {
		t.Fatalf("got %q, want %q", recvData, testData)
	}
}
