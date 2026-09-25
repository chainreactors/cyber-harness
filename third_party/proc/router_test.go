package proc

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"sync"
	"testing"
	"time"
)

type fakeSession struct {
	info    Info
	output  []byte
	done    chan struct{}
	writes  [][]byte
	resizes [][2]int
	killed  bool
}

type fakeManager struct {
	mu       sync.Mutex
	sessions map[string]*fakeSession
}

func newFakeManager() *fakeManager {
	return &fakeManager{sessions: make(map[string]*fakeSession)}
}

func (m *fakeManager) add(id, name string, output []byte) Info {
	return m.addKind(id, "", name, output)
}

func (m *fakeManager) addKind(id, kind, name string, output []byte) Info {
	info := Info{ID: id, Name: name, State: StateRunning}
	if kind != "" {
		info.Kind = kind
	}
	m.sessions[id] = &fakeSession{info: info, output: append([]byte(nil), output...), done: make(chan struct{})}
	return info
}

func (m *fakeManager) List() []Info {
	out := make([]Info, 0, len(m.sessions))
	for _, s := range m.sessions {
		out = append(out, s.info)
	}
	return out
}

func (m *fakeManager) Get(id string) (Info, bool) {
	s, ok := m.sessions[id]
	if !ok {
		return Info{}, false
	}
	return s.info, true
}

func (m *fakeManager) Write(id string, data []byte) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return errors.New("missing session")
	}
	s.writes = append(s.writes, append([]byte(nil), data...))
	return nil
}

func (m *fakeManager) writesFor(id string) [][]byte {
	m.mu.Lock()
	defer m.mu.Unlock()
	return append([][]byte(nil), m.sessions[id].writes...)
}

func (m *fakeManager) Resize(id string, cols, rows int) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return errors.New("missing session")
	}
	s.resizes = append(s.resizes, [2]int{cols, rows})
	return nil
}

func (m *fakeManager) Kill(id string) error {
	m.mu.Lock()
	defer m.mu.Unlock()
	s := m.sessions[id]
	if s == nil {
		return errors.New("missing session")
	}
	s.killed = true
	s.info.State = StateKilled
	close(s.done)
	return nil
}

func (m *fakeManager) killedFlag(id string) bool {
	m.mu.Lock()
	defer m.mu.Unlock()
	return m.sessions[id].killed
}

func (m *fakeManager) SnapshotBytes(id string, n int) ([]byte, int64, error) {
	s := m.sessions[id]
	if s == nil {
		return nil, 0, errors.New("missing session")
	}
	data := s.output
	if n > 0 && len(data) > n {
		data = data[len(data)-n:]
	}
	return append([]byte(nil), data...), int64(len(s.output)), nil
}

func (m *fakeManager) MonitorFrom(ctx context.Context, id string, offset int64, interval time.Duration, push func([]byte)) error {
	if m.sessions[id] == nil {
		return errors.New("missing session")
	}
	return nil
}

func (m *fakeManager) Wait(ctx context.Context, id string, timeout time.Duration) (Info, error) {
	s := m.sessions[id]
	if s == nil {
		return Info{}, errors.New("missing session")
	}
	select {
	case <-ctx.Done():
		return s.info, ctx.Err()
	case <-s.done:
		return s.info, nil
	}
}

func TestRouterServeWithNetPipe(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
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

	router := NewRouter(mgr, WithOpener("repl", func(_ context.Context, spec OpenSpec) (OpenResult, error) {
		info := mgr.add("session-1", spec.Name, []byte("snapshot\n"))
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

	serverConn, clientConn := net.Pipe()
	enc := json.NewEncoder(clientConn)
	dec := json.NewDecoder(clientConn)

	done := make(chan error, 1)
	go func() {
		done <- router.Serve(ctx, serverConn)
	}()

	writeFrame := func(f Frame) {
		t.Helper()
		if err := enc.Encode(f); err != nil {
			t.Fatalf("encode frame: %v", err)
		}
	}
	readFrameJSON := func(typ FrameType) Frame {
		t.Helper()
		var f Frame
		if err := dec.Decode(&f); err != nil {
			t.Fatalf("decode frame: %v", err)
		}
		if f.Type != typ {
			t.Fatalf("expected %s, got %+v", typ, f)
		}
		return f
	}

	writeFrame(Frame{Type: FrameOpen, StreamID: "local", Kind: "repl", Name: "test-repl", Cols: 100, Rows: 30})
	opened := readFrameJSON(FrameOpened)
	if opened.StreamID != "local" || opened.SessionID != "session-1" {
		t.Fatalf("unexpected opened frame: %+v", opened)
	}

	writeFrame(Frame{Type: FrameInput, StreamID: "local", Data: []byte("abc")})
	waitUntilRouter(t, time.Second, func() bool {
		return len(mgr.writesFor("session-1")) == 1
	})
	if got := string(mgr.writesFor("session-1")[0]); got != "abc" {
		t.Fatalf("unexpected write: %q", got)
	}

	writeFrame(Frame{Type: FrameResize, StreamID: "local", Cols: 120, Rows: 40})
	waitUntilRouter(t, time.Second, func() bool {
		return len(getResized()) == 2
	})
	if got := getResized(); len(got) != 2 || got[1] != [2]int{120, 40} {
		t.Fatalf("resize control not called: %+v", got)
	}

	writeFrame(Frame{Type: FrameDetach, StreamID: "local"})
	readFrameJSON(FrameDetached)
	if mgr.killedFlag("session-1") {
		t.Fatal("detach killed session")
	}

	clientConn.Close()
	select {
	case err := <-done:
		if err != nil {
			t.Fatalf("serve returned error: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("serve did not stop after conn closed")
	}
}

func TestRouterListAttachAndKill(t *testing.T) {
	mgr := newFakeManager()
	mgr.add("session-1", "existing", []byte("attach_ok\n"))
	router := NewRouter(mgr)
	defer router.Close()

	out := make(chan Frame, 8)
	send := func(frame Frame) { out <- frame }

	router.Handle(context.Background(), Frame{Type: FrameList, StreamID: "term"}, send)
	sessions := readFrame(t, out, FrameSessions)
	if len(sessions.Sessions) != 1 || sessions.Sessions[0].ID != "session-1" {
		t.Fatalf("unexpected sessions: %+v", sessions.Sessions)
	}

	router.Handle(context.Background(), Frame{Type: FrameAttach, StreamID: "term", SessionID: "session-1"}, send)
	readFrame(t, out, FrameAttached)
	output := readFrame(t, out, FrameOutput)
	if string(output.Data) != "attach_ok\n" {
		t.Fatalf("unexpected attach output: %q", output.Data)
	}

	router.Handle(context.Background(), Frame{Type: FrameKill, StreamID: "term"}, send)
	if !mgr.killedFlag("session-1") {
		t.Fatal("kill did not call manager")
	}
}

func TestRouterSingletonOpenReusesRunningSession(t *testing.T) {
	mgr := newFakeManager()
	openCount := 0
	router := NewRouter(mgr, WithOpener("repl", func(_ context.Context, spec OpenSpec) (OpenResult, error) {
		openCount++
		info := mgr.addKind("session-1", "repl", spec.Name, nil)
		return OpenResult{Info: info}, nil
	}))
	defer router.Close()

	out := make(chan Frame, 8)
	send := func(frame Frame) { out <- frame }

	router.Handle(context.Background(), Frame{Type: FrameOpen, StreamID: "term-1", Kind: "repl", Name: "main-repl", Singleton: true}, send)
	opened := readFrame(t, out, FrameOpened)
	if opened.SessionID != "session-1" || openCount != 1 {
		t.Fatalf("unexpected first open: frame=%+v openCount=%d", opened, openCount)
	}

	router.Handle(context.Background(), Frame{Type: FrameOpen, StreamID: "term-2", Kind: "repl", Name: "main-repl", Singleton: true}, send)
	attached := readFrame(t, out, FrameAttached)
	if attached.SessionID != "session-1" {
		t.Fatalf("singleton open attached wrong session: %+v", attached)
	}
	if openCount != 1 {
		t.Fatalf("singleton open called opener again: %d", openCount)
	}
	if mgr.killedFlag("session-1") {
		t.Fatal("singleton reattach killed the existing session")
	}
}

func TestRouterTracksActiveStreams(t *testing.T) {
	mgr := newFakeManager()
	router := NewRouter(mgr)

	out := make(chan Frame, 8)
	send := func(frame Frame) { out <- frame }

	router.Handle(context.Background(), Frame{Type: FrameList, StreamID: "term-1"}, send)
	readFrame(t, out, FrameSessions)
	assertStreamIDs(t, router.StreamIDs(), "term-1")

	router.Handle(context.Background(), Frame{Type: FrameList, StreamID: "term-2"}, send)
	readFrame(t, out, FrameSessions)
	assertStreamIDs(t, router.StreamIDs(), "term-1", "term-2")

	router.Handle(context.Background(), Frame{Type: FrameDetach, StreamID: "term-1"}, send)
	readFrame(t, out, FrameDetached)
	assertStreamIDs(t, router.StreamIDs(), "term-2")

	router.Close()
	assertStreamIDs(t, router.StreamIDs())
}

func TestRouterHandlePanicRecovery(t *testing.T) {
	mgr := newFakeManager()
	router := NewRouter(mgr, WithOpener("bad", func(_ context.Context, _ OpenSpec) (OpenResult, error) {
		panic("boom")
	}))
	defer router.Close()

	out := make(chan Frame, 8)
	send := func(f Frame) { out <- f }

	router.Handle(context.Background(), Frame{Type: FrameOpen, StreamID: "s1", Kind: "bad"}, send)
	errFrame := readFrame(t, out, FrameError)
	if errFrame.Error == "" || errFrame.StreamID != "s1" {
		t.Fatalf("expected panic error, got %+v", errFrame)
	}

	mgr.add("sess-1", "ok", nil)
	router.Handle(context.Background(), Frame{Type: FrameList, StreamID: "s1"}, send)
	sessions := readFrame(t, out, FrameSessions)
	if len(sessions.Sessions) != 1 {
		t.Fatal("router broken after panic recovery")
	}
}

func assertStreamIDs(t *testing.T, got []string, want ...string) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("stream ids = %v, want %v", got, want)
	}
	set := make(map[string]bool, len(got))
	for _, id := range got {
		set[id] = true
	}
	for _, id := range want {
		if !set[id] {
			t.Fatalf("stream ids = %v, want %v", got, want)
		}
	}
}

func readFrame(t *testing.T, ch <-chan Frame, typ FrameType) Frame {
	t.Helper()
	select {
	case frame := <-ch:
		if frame.Type != typ {
			t.Fatalf("expected %s, got %+v", typ, frame)
		}
		return frame
	case <-time.After(time.Second):
		t.Fatalf("timeout waiting for %s", typ)
		return Frame{}
	}
}

func waitUntilRouter(t *testing.T, timeout time.Duration, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatalf("condition not met within %s", timeout)
		}
		time.Sleep(10 * time.Millisecond)
	}
}
