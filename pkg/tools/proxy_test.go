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

	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tools/gogo"
	"github.com/chainreactors/aiscan/pkg/tools/neutron"
	"github.com/chainreactors/aiscan/pkg/tools/scan"
	"github.com/chainreactors/aiscan/pkg/tools/spray"
	"github.com/chainreactors/aiscan/pkg/tools/zombie"
	gogopkg "github.com/chainreactors/gogo/v2/pkg"
	neutronhttp "github.com/chainreactors/neutron/protocols/http"
	"github.com/chainreactors/proxyclient"
	zombiepkg "github.com/chainreactors/zombie/pkg"
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
			// Minimal SOCKS5 handshake: read greeting, send no-auth, read connect, send success
			go handleSOCKS5(conn)
		}
	}()
	t.Cleanup(func() { ln.Close() })
	return fmt.Sprintf("socks5://%s", ln.Addr().String()), func() int32 { return count.Load() }
}

func handleSOCKS5(conn net.Conn) {
	defer conn.Close()
	buf := make([]byte, 256)
	// Read greeting: VER NMETHODS METHODS...
	n, err := conn.Read(buf)
	if err != nil || n < 3 || buf[0] != 0x05 {
		return
	}
	// Send no-auth response
	conn.Write([]byte{0x05, 0x00})

	// Read connect request: VER CMD RSV ATYP DST.ADDR DST.PORT
	n, err = conn.Read(buf)
	if err != nil || n < 7 || buf[0] != 0x05 || buf[1] != 0x01 {
		return
	}

	// Parse destination for logging, then send success response
	// Response: VER REP RSV ATYP BND.ADDR BND.PORT
	conn.Write([]byte{0x05, 0x00, 0x00, 0x01, 0x00, 0x00, 0x00, 0x00, 0x00, 0x00})

	// Relay/drain briefly so the dialer sees a connected socket
	go io.Copy(io.Discard, conn)
	time.Sleep(50 * time.Millisecond)
}

// TestProxyclientDialCreateFromURL verifies that proxyclient can parse our
// proxy URLs and produce a working Dial function.
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
	// The connection may fail because 127.0.0.1:1 is not listening,
	// but the SOCKS5 proxy must have received the connect attempt.
	if getCount() == 0 {
		t.Fatal("proxyclient dial did not reach the SOCKS5 proxy")
	}
	_ = err // expected to fail at the remote end
}

// TestGogoInjectProxy verifies that the gogo command injects --proxy args.
func TestGogoInjectProxy(t *testing.T) {
	proxyAddr, _ := startSOCKS5CountingProxy(t)

	cmd := gogo.New(nil).WithProxy(proxyAddr)

	// Use --help to avoid actually scanning, but verify proxy is passed
	// by checking that the command doesn't error on the proxy flag itself.
	out, err := cmd.Execute(context.Background(), []string{"--help"})
	if err != nil {
		t.Fatalf("gogo --help with proxy: %v", err)
	}
	if out == "" {
		t.Fatal("expected help output")
	}

	// Verify injectProxy logic: manually call the method to check args
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

	// Verify no double injection
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

// TestSprayInjectProxy verifies that the spray command injects --proxy args.
func TestSprayInjectProxy(t *testing.T) {
	proxyAddr, _ := startSOCKS5CountingProxy(t)

	cmd := spray.New(nil).WithProxy(proxyAddr)

	// Verify injectProxy logic
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

// TestZombieInstallProxy verifies that zombie's proxy sets the global
// ProxyDialTimeout via proxyclient and restores it after.
func TestZombieInstallProxy(t *testing.T) {
	proxyAddr, getCount := startSOCKS5CountingProxy(t)

	// Save original state
	origProxy := zombiepkg.ProxyDialTimeout

	cmd := zombie.New(nil).WithProxy(proxyAddr)

	// Execute with --help to trigger installProxy
	_, err := cmd.Execute(context.Background(), []string{"--help"})
	if err != nil {
		t.Fatalf("zombie --help: %v", err)
	}

	// After Execute returns, proxy should be restored
	if zombiepkg.ProxyDialTimeout != nil && origProxy == nil {
		t.Fatal("zombie proxy was not restored after Execute")
	}

	// Now verify the proxy was installed during execution
	// by calling installProxy manually and using the dial function
	restore := cmd.TestInstallProxy()
	if zombiepkg.ProxyDialTimeout == nil {
		t.Fatal("zombie ProxyDialTimeout not set after installProxy")
	}

	// Dial through the proxy
	conn, err := zombiepkg.ProxyDialTimeout("tcp", "127.0.0.1:1", 2*time.Second)
	if conn != nil {
		conn.Close()
	}
	// Connection to 127.0.0.1:1 will fail, but the proxy should have been contacted
	if getCount() == 0 {
		t.Fatal("zombie ProxyDialTimeout did not route through the SOCKS5 proxy")
	}

	restore()
	if origProxy == nil && zombiepkg.ProxyDialTimeout != nil {
		t.Fatal("zombie proxy was not properly restored")
	}
}

// TestNeutronInstallProxy verifies that neutron sets the DefaultTransport
// proxy via proxyclient and restores it after.
func TestNeutronInstallProxy(t *testing.T) {
	proxyAddr, _ := startSOCKS5CountingProxy(t)

	// Save original state
	origProxy := neutronhttp.DefaultOption.Proxy
	origTransport := neutronhttp.DefaultTransport.Proxy

	cmd := neutron.New(nil, nil).WithProxy(proxyAddr)

	restore := cmd.TestInstallProxy()

	// Verify proxy was set
	if neutronhttp.DefaultOption.Proxy == nil {
		t.Fatal("neutron DefaultOption.Proxy not set")
	}
	if neutronhttp.DefaultTransport.Proxy == nil {
		t.Fatal("neutron DefaultTransport.Proxy not set")
	}

	restore()

	// Verify restoration
	if fmt.Sprintf("%p", neutronhttp.DefaultOption.Proxy) != fmt.Sprintf("%p", origProxy) {
		t.Fatal("neutron DefaultOption.Proxy not restored")
	}
	if fmt.Sprintf("%p", neutronhttp.DefaultTransport.Proxy) != fmt.Sprintf("%p", origTransport) {
		t.Fatal("neutron DefaultTransport.Proxy not restored")
	}
}

// TestScanInstallProxy verifies that the scan pipeline's installProxy
// sets global proxies for gogo, neutron, and zombie simultaneously.
func TestScanInstallProxy(t *testing.T) {
	proxyAddr, getCount := startSOCKS5CountingProxy(t)

	// Save original state
	origGogoDialCtx := gogopkg.DefaultTransport.DialContext
	origGogoProxy := gogopkg.ProxyDialTimeout
	origNeutronProxy := neutronhttp.DefaultOption.Proxy
	origZombieProxy := zombiepkg.ProxyDialTimeout

	// Use the scan command's proxy installer directly
	scanCmd := scan.New(nil, scan.WithProxy(proxyAddr), scan.WithLogger(telemetry.NopLogger()))
	restore := scanCmd.TestInstallProxy()

	// Verify gogo globals
	if gogopkg.DefaultTransport.DialContext == nil {
		t.Fatal("gogo DefaultTransport.DialContext not set")
	}
	if gogopkg.ProxyDialTimeout == nil {
		t.Fatal("gogo ProxyDialTimeout not set")
	}

	// Verify neutron globals
	if neutronhttp.DefaultOption.Proxy == nil {
		t.Fatal("neutron DefaultOption.Proxy not set")
	}

	// Verify zombie globals
	if zombiepkg.ProxyDialTimeout == nil {
		t.Fatal("zombie ProxyDialTimeout not set")
	}

	// Actually dial through gogo proxy to verify it reaches our SOCKS5 server
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	conn, err := gogopkg.DefaultTransport.DialContext(ctx, "tcp", "127.0.0.1:1")
	if conn != nil {
		conn.Close()
	}
	_ = err

	if getCount() == 0 {
		t.Fatal("gogo proxy dial did not route through the SOCKS5 proxy")
	}

	restore()

	// Verify restoration
	if fmt.Sprintf("%p", gogopkg.DefaultTransport.DialContext) != fmt.Sprintf("%p", origGogoDialCtx) {
		t.Fatal("gogo DefaultTransport.DialContext not restored")
	}
	if fmt.Sprintf("%p", gogopkg.ProxyDialTimeout) != fmt.Sprintf("%p", origGogoProxy) {
		t.Fatal("gogo ProxyDialTimeout not restored")
	}
	if fmt.Sprintf("%p", neutronhttp.DefaultOption.Proxy) != fmt.Sprintf("%p", origNeutronProxy) {
		t.Fatal("neutron DefaultOption.Proxy not restored")
	}
	if fmt.Sprintf("%p", zombiepkg.ProxyDialTimeout) != fmt.Sprintf("%p", origZombieProxy) {
		t.Fatal("zombie ProxyDialTimeout not restored")
	}
}
