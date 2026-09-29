package bridge

import (
	"bytes"
	"fmt"
	"io"
	"net"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"
)

func TestSocks5ListenIsLoopbackOnly(t *testing.T) {
	for _, addr := range []string{"127.0.0.1:19099", "[::1]:19099", "localhost:19099"} {
		if err := validateSocks5ListenAddr(addr); err != nil {
			t.Errorf("%s should be allowed: %v", addr, err)
		}
	}
	for _, addr := range []string{"0.0.0.0:19099", ":19099", "[::]:19099", "192.0.2.1:19099", "example.com:19099", "not-an-address"} {
		if err := validateSocks5ListenAddr(addr); err == nil {
			t.Errorf("%s should be rejected", addr)
		}
	}
	if err := NewSocks5Server("0.0.0.0:0", 0, 0, nil).ListenAndServe(); err == nil {
		t.Fatal("server must reject a wildcard bind before listening")
	}
}

func TestSocks5RejectsUnsupportedAuthMethods(t *testing.T) {
	server, client := net.Pipe()
	defer client.Close()
	go NewSocks5Server("127.0.0.1:0", 0, 0, nil).handleConn(server)
	if _, err := client.Write([]byte{0x05, 0x01, 0x02}); err != nil {
		t.Fatal(err)
	}
	reply := make([]byte, 2)
	if _, err := io.ReadFull(client, reply); err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(reply, []byte{0x05, 0xff}) {
		t.Fatalf("unsupported authentication should be rejected, got %x", reply)
	}
}

func TestSocks5ServerIntegration(t *testing.T) {
	// 1. Create a dummy HTTP backend
	backend := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("PROXY_BACKEND_OK"))
	}))
	defer backend.Close()

	// 2. Pick a random loopback port for SOCKS5
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("failed to listen on random port: %v", err)
	}
	socksAddr := ln.Addr().String()
	_ = ln.Close()

	srv := NewSocks5Server(socksAddr, 5*time.Second, 5*time.Second, nil)
	go func() {
		_ = srv.ListenAndServe()
	}()
	defer srv.Close()

	// Wait for server to listen
	time.Sleep(50 * time.Millisecond)

	// 3. Make HTTP request through SOCKS5 proxy
	proxyURL, err := url.Parse(fmt.Sprintf("socks5://%s", socksAddr))
	if err != nil {
		t.Fatalf("failed to parse proxy URL: %v", err)
	}

	client := &http.Client{
		Transport: &http.Transport{
			Proxy: http.ProxyURL(proxyURL),
		},
		Timeout: 5 * time.Second,
	}

	resp, err := client.Get(backend.URL)
	if err != nil {
		t.Fatalf("client.Get through socks5 failed: %v", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		t.Fatalf("expected status 200, got %d", resp.StatusCode)
	}

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		t.Fatalf("failed to read body: %v", err)
	}
	if string(body) != "PROXY_BACKEND_OK" {
		t.Fatalf("unexpected body: %q", string(body))
	}
}
