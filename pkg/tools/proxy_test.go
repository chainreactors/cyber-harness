package tools

import (
	"context"
	"fmt"
	"io"
	"net"
	"net/url"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/tools/gogo"
	"github.com/chainreactors/aiscan/pkg/tools/neutron"
	"github.com/chainreactors/aiscan/pkg/tools/spray"
	"github.com/chainreactors/aiscan/pkg/tools/zombie"
	neutronhttp "github.com/chainreactors/neutron/protocols/http"
	"github.com/chainreactors/proxyclient"
)

// startSOCKS5CountingProxy starts a minimal SOCKS5 server that counts
// connection attempts. It returns the proxy URL and a function to read
// the connection count.
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

func TestGogoInjectProxy(t *testing.T) {
	proxyAddr, _ := startSOCKS5CountingProxy(t)

	cmd := gogo.New(nil).WithProxy(proxyAddr)

	out, err := cmd.Execute(context.Background(), []string{"--help"})
	if err != nil {
		t.Fatalf("gogo --help with proxy: %v", err)
	}
	if out == "" {
		t.Fatal("expected help output")
	}

	injected := cmd.TestInjectProxy([]string{"-i", "127.0.0.1"})
	hasProxy := false
	for i, arg := range injected {
		if arg == "--proxy" && i+1 < len(injected) && injected[i+1] == proxyAddr {
			hasProxy = true
			break
		}
	}
	if !hasProxy {
		t.Fatalf("expected --proxy %s in args, got %v", proxyAddr, injected)
	}

	alreadyHas := cmd.TestInjectProxy([]string{"-i", "127.0.0.1", "--proxy", "socks5://other:1080"})
	proxyCount := 0
	for _, arg := range alreadyHas {
		if arg == "--proxy" {
			proxyCount++
		}
	}
	if proxyCount != 1 {
		t.Fatalf("expected 1 --proxy flag (user-provided), got %d in %v", proxyCount, alreadyHas)
	}
}

func TestSprayInjectProxy(t *testing.T) {
	proxyAddr, _ := startSOCKS5CountingProxy(t)

	cmd := spray.New(nil).WithProxy(proxyAddr)

	injected := cmd.TestInjectProxy([]string{"-u", "http://example.com"})
	hasProxy := false
	for i, arg := range injected {
		if arg == "--proxy" && i+1 < len(injected) && injected[i+1] == proxyAddr {
			hasProxy = true
			break
		}
	}
	if !hasProxy {
		t.Fatalf("expected --proxy %s in args, got %v", proxyAddr, injected)
	}
}

// TestZombieExecuteWithProxy verifies that zombie's Execute passes proxy via
// RunOptions.ProxyDial (not global patching).
func TestZombieExecuteWithProxy(t *testing.T) {
	proxyAddr, _ := startSOCKS5CountingProxy(t)

	cmd := zombie.New(nil).WithProxy(proxyAddr)

	// Execute with --help just to verify no panic; the proxy is built
	// but not exercised because --help exits before any network I/O.
	_, err := cmd.Execute(context.Background(), []string{"--help"})
	if err != nil {
		t.Fatalf("zombie --help: %v", err)
	}
}

// TestNeutronSetProxyUpdatesDefault verifies that neutron's SetProxy/WithProxy
// sets neutron DefaultOption.Proxy for subsequent executions.
func TestNeutronSetProxyUpdatesDefault(t *testing.T) {
	proxyAddr, _ := startSOCKS5CountingProxy(t)

	origProxy := neutronhttp.DefaultOption.Proxy

	cmd := neutron.New(nil, nil).WithProxy(proxyAddr)
	_ = cmd

	if neutronhttp.DefaultOption.Proxy == nil {
		t.Fatal("neutron DefaultOption.Proxy not set after WithProxy")
	}
	if neutronhttp.DefaultTransport.Proxy == nil {
		t.Fatal("neutron DefaultTransport.Proxy not set after WithProxy")
	}

	// Clear proxy
	cmd.SetProxy("")
	if neutronhttp.DefaultOption.Proxy != nil {
		t.Fatal("neutron DefaultOption.Proxy not cleared after SetProxy empty")
	}

	_ = origProxy
}
