package engine_test

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/proxyclient"
)

func startSOCKS5CountingProxy(t *testing.T) (string, func() int32) {
	t.Helper()
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatalf("listen: %v", err)
	}
	var count atomic.Int32
	go func() {
		for {
			conn, err := ln.Accept()
			if err != nil {
				return
			}
			count.Add(1)
			go handleSOCKS5(conn)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return fmt.Sprintf("socks5://%s", ln.Addr().String()), func() int32 { return count.Load() }
}

func handleSOCKS5(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 256)
	n, err := conn.Read(buf)
	if err != nil || n < 3 || buf[0] != 0x05 {
		return
	}
	conn.Write([]byte{0x05, 0x00})

	n, err = conn.Read(buf)
	if err != nil || n < 7 || buf[0] != 0x05 || buf[1] != 0x01 {
		return
	}

	conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})

	go io.Copy(io.Discard, conn)
	time.Sleep(50 * time.Millisecond)
}

func TestProxyclientDialCreateFromURL(t *testing.T) {
	proxyAddr, getCount := startSOCKS5CountingProxy(t)

	proxyURL, err := url.Parse(proxyAddr)
	if err != nil {
		t.Fatalf("parse proxy URL: %v", err)
	}
	dial, err := proxyclient.NewClient(proxyURL)
	if err != nil {
		t.Fatalf("proxyclient.NewClient: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := dial.DialContext(ctx, "tcp", "127.0.0.1:1")
	if conn != nil {
		conn.Close()
	}
	if getCount() == 0 {
		t.Fatal("proxyclient dial did not reach the SOCKS5 proxy")
	}
	_ = err
}
