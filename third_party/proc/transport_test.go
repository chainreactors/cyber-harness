package proc

import (
	"context"
	"encoding/json"
	"io"
	"net"
	"runtime"
	"sync"
	"testing"
	"time"
)

func testConnRoundTrip(t *testing.T, serverConn, clientConn io.ReadWriteCloser) {
	t.Helper()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	mgr := newFakeManager()
	var (
		resizeMu sync.Mutex
		resized  [][2]int
	)
	getResized := func() [][2]int {
		resizeMu.Lock()
		defer resizeMu.Unlock()
		return append([][2]int(nil), resized...)
	}

	router := NewRouter(mgr, WithOpener("shell", func(_ context.Context, spec OpenSpec) (OpenResult, error) {
		info := mgr.add("sess-1", spec.Name, []byte("hello\n"))
		return OpenResult{
			Info: info,
			Resize: func(cols, rows int) {
				resizeMu.Lock()
				resized = append(resized, [2]int{cols, rows})
				resizeMu.Unlock()
			},
		}, nil
	}))
	defer router.Close()

	serveDone := make(chan error, 1)
	go func() {
		serveDone <- router.Serve(ctx, serverConn)
	}()

	enc := json.NewEncoder(clientConn)
	dec := json.NewDecoder(clientConn)

	write := func(f Frame) {
		t.Helper()
		if err := enc.Encode(f); err != nil {
			t.Fatalf("encode: %v", err)
		}
	}
	read := func(typ FrameType) Frame {
		t.Helper()
		var f Frame
		if err := dec.Decode(&f); err != nil {
			t.Fatalf("decode: %v", err)
		}
		if f.Type != typ {
			t.Fatalf("expected %s, got %s", typ, f.Type)
		}
		return f
	}

	// open
	write(Frame{Type: FrameOpen, StreamID: "s1", Kind: "shell", Name: "test", Cols: 80, Rows: 24})
	opened := read(FrameOpened)
	if opened.SessionID != "sess-1" {
		t.Fatalf("unexpected session: %s", opened.SessionID)
	}

	// input
	write(Frame{Type: FrameInput, StreamID: "s1", Data: []byte("xyz")})
	waitUntilRouter(t, time.Second, func() bool {
		return len(mgr.writesFor("sess-1")) == 1
	})
	if got := string(mgr.writesFor("sess-1")[0]); got != "xyz" {
		t.Fatalf("unexpected write: %q", got)
	}

	// resize
	write(Frame{Type: FrameResize, StreamID: "s1", Cols: 120, Rows: 40})
	waitUntilRouter(t, time.Second, func() bool {
		return len(getResized()) >= 2
	})
	if got := getResized(); got[len(got)-1] != [2]int{120, 40} {
		t.Fatalf("unexpected resize: %v", got)
	}

	// list
	write(Frame{Type: FrameList, StreamID: "s1"})
	sessions := read(FrameSessions)
	if len(sessions.Sessions) != 1 {
		t.Fatalf("expected 1 session, got %d", len(sessions.Sessions))
	}

	// detach
	write(Frame{Type: FrameDetach, StreamID: "s1"})
	read(FrameDetached)

	// close client -> server returns
	clientConn.Close()
	select {
	case err := <-serveDone:
		if err != nil {
			t.Fatalf("serve error: %v", err)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("serve did not stop")
	}
}

func TestConnTransport(t *testing.T) {
	t.Run("NetPipe", func(t *testing.T) {
		server, client := net.Pipe()
		testConnRoundTrip(t, server, client)
	})

	t.Run("TCP", func(t *testing.T) {
		listener, err := net.Listen("tcp", "127.0.0.1:0")
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()

		accepted := make(chan net.Conn, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}()

		client, err := net.Dial("tcp", listener.Addr().String())
		if err != nil {
			t.Fatal(err)
		}

		testConnRoundTrip(t, <-accepted, client)
	})

	t.Run("UnixSocket", func(t *testing.T) {
		if runtime.GOOS == "windows" {
			t.Skip("unix sockets not available on windows")
		}
		socketPath := t.TempDir() + "/test.sock"
		listener, err := net.Listen("unix", socketPath)
		if err != nil {
			t.Fatal(err)
		}
		defer listener.Close()

		accepted := make(chan net.Conn, 1)
		go func() {
			conn, err := listener.Accept()
			if err != nil {
				return
			}
			accepted <- conn
		}()

		client, err := net.Dial("unix", socketPath)
		if err != nil {
			t.Fatal(err)
		}

		testConnRoundTrip(t, <-accepted, client)
	})
}
