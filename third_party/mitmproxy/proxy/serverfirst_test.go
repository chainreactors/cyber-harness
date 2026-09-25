package proxy

import (
	"bufio"
	"fmt"
	"io"
	"net"
	"strings"
	"testing"
	"time"
)

// TestServerFirstSlowBannerNoDeadlock is a regression test for a deadlock in
// dispatchTunnel: when a server connection exists but its server-first probe
// returns nothing within peekTimeout (a slow/idle server), the client-side
// Peek was issued with no read deadline. For a server-first protocol the
// client is itself waiting for the server banner and never sends the bytes
// Peek wants, so Peek blocked forever. The banner is delayed past peekTimeout
// here to force the probe to come up empty; without the fix the tunnel never
// delivers it and the read below times out.
func TestServerFirstSlowBannerNoDeadlock(t *testing.T) {
	server, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		t.Fatal(err)
	}
	defer server.Close()
	go func() {
		for {
			c, err := server.Accept()
			if err != nil {
				return
			}
			go func(c net.Conn) {
				// Banner arrives AFTER the server-first probe window.
				time.Sleep(peekTimeout + 300*time.Millisecond)
				c.Write([]byte("SSH-2.0-DelayedServer\r\n"))
				io.Copy(io.Discard, c)
				c.Close()
			}(c)
		}
	}()

	p, err := NewProxy(&Options{Addr: "127.0.0.1:0", SslInsecure: true, StreamLargeBodies: 10 << 20})
	if err != nil {
		t.Fatal(err)
	}
	paddr, _, err := p.StartAsync()
	if err != nil {
		t.Fatal(err)
	}

	pc, err := net.Dial("tcp", paddr.String())
	if err != nil {
		t.Fatal(err)
	}
	defer pc.Close()

	target := server.Addr().String()
	fmt.Fprintf(pc, "CONNECT %s HTTP/1.1\r\nHost: %s\r\n\r\n", target, target)

	br := bufio.NewReader(pc)
	status, err := br.ReadString('\n')
	if err != nil {
		t.Fatalf("read CONNECT status: %v", err)
	}
	if !strings.Contains(status, "200") {
		t.Fatalf("CONNECT not established: %q", status)
	}
	for {
		line, err := br.ReadString('\n')
		if err != nil {
			t.Fatalf("read CONNECT headers: %v", err)
		}
		if line == "\r\n" || line == "\n" {
			break
		}
	}

	pc.SetReadDeadline(time.Now().Add(5 * time.Second))
	buf := make([]byte, 64)
	n, err := io.ReadAtLeast(br, buf, 3)
	if err != nil {
		t.Fatalf("server-first banner never delivered (peek deadlock regression): %v", err)
	}
	if !strings.Contains(string(buf[:n]), "SH-") {
		t.Fatalf("unexpected banner: %q", string(buf[:n]))
	}
	t.Logf("OK: delivered %q", string(buf[:n]))
}
