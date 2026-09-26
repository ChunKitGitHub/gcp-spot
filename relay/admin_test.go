package main

import (
	"crypto/rand"
	"crypto/rsa"
	"crypto/tls"
	"crypto/x509"
	"crypto/x509/pkix"
	"io"
	"math/big"
	"net"
	"net/http"
	"testing"
	"time"
)

func generateTestCert() (*tls.Config, error) {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		return nil, err
	}
	template := x509.Certificate{
		SerialNumber: big.NewInt(1),
		Subject:      pkix.Name{CommonName: "127.0.0.1"},
		IPAddresses:  []net.IP{net.ParseIP("127.0.0.1")},
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(time.Hour),
		KeyUsage:     x509.KeyUsageKeyEncipherment | x509.KeyUsageDigitalSignature,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	certDER, err := x509.CreateCertificate(rand.Reader, &template, &template, &key.PublicKey, key)
	if err != nil {
		return nil, err
	}
	cert := tls.Certificate{
		Certificate: [][]byte{certDER},
		PrivateKey:  key,
	}
	return &tls.Config{Certificates: []tls.Certificate{cert}}, nil
}

func TestDualHTTPAndHTTPSListener(t *testing.T) {
	tlsConf, err := generateTestCert()
	if err != nil {
		t.Fatalf("generate cert: %v", err)
	}

	rawListener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	defer rawListener.Close()

	dual := &dualListener{
		Listener:  rawListener,
		tlsConfig: tlsConf,
	}

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/plain")
		if r.TLS != nil {
			_, _ = w.Write([]byte("hello https"))
		} else {
			_, _ = w.Write([]byte("hello http"))
		}
	})

	server := &http.Server{Handler: handler}
	go func() {
		_ = server.Serve(dual)
	}()
	defer server.Close()

	addr := rawListener.Addr().String()

	// 1. 测试明文 HTTP 访问
	httpClient := &http.Client{Timeout: 3 * time.Second}
	respHTTP, err := httpClient.Get("http://" + addr)
	if err != nil {
		t.Fatalf("http get: %v", err)
	}
	defer respHTTP.Body.Close()
	bodyHTTP, _ := io.ReadAll(respHTTP.Body)
	if string(bodyHTTP) != "hello http" {
		t.Fatalf("expected 'hello http', got %q", string(bodyHTTP))
	}

	// 2. 测试 HTTPS 访问
	httpsClient := &http.Client{
		Timeout: 3 * time.Second,
		Transport: &http.Transport{
			TLSClientConfig: &tls.Config{InsecureSkipVerify: true},
		},
	}
	respHTTPS, err := httpsClient.Get("https://" + addr)
	if err != nil {
		t.Fatalf("https get: %v", err)
	}
	defer respHTTPS.Body.Close()
	bodyHTTPS, _ := io.ReadAll(respHTTPS.Body)
	if string(bodyHTTPS) != "hello https" {
		t.Fatalf("expected 'hello https', got %q", string(bodyHTTPS))
	}
}
