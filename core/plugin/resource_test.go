package plugin_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/plugin"
)

// These fixtures own real resources; all scheduling gates remain test-only.
type fileModule struct {
	path         string
	file         *os.File
	opens        int
	closes       int
	loadErr      error
	cleanupReady <-chan struct{}
}

func (m *fileModule) Load(ctx context.Context) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f, err := os.OpenFile(m.path, os.O_CREATE|os.O_EXCL|os.O_RDWR, 0o600)
	if err != nil {
		return err
	}
	m.file = f
	m.opens++
	return m.loadErr
}

func (m *fileModule) Close(ctx context.Context) error {
	if m.file == nil {
		return nil
	}
	if m.cleanupReady != nil {
		select {
		case <-m.cleanupReady:
		case <-ctx.Done():
			return ctx.Err()
		}
	}
	if err := m.file.Close(); err != nil {
		return err
	}
	m.closes++
	return nil
}

var errNotAccepting = errors.New("writer is not accepting work")

type writerModule struct {
	file *fileModule

	mu        sync.Mutex
	ctx       context.Context
	cancel    context.CancelFunc
	accepting bool
	stopping  bool
	inflight  int
	drained   chan struct{}
	stopped   chan struct{}

	// A test can hold an accepted write and its cleanup independently.
	accepted     chan struct{}
	writeReady   <-chan struct{}
	cleanupReady <-chan struct{}
}

func newWriterModule(file *fileModule) *writerModule {
	return &writerModule{file: file, drained: make(chan struct{}), stopped: make(chan struct{})}
}

func (m *writerModule) Load(ctx context.Context) error {
	if _, err := m.file.file.Stat(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Initialization cancellation is distinct from the module's lifetime.
	m.ctx, m.cancel = context.WithCancel(context.WithoutCancel(ctx))
	m.accepting = true
	return nil
}

func (m *writerModule) Write(ctx context.Context, text string) error {
	m.mu.Lock()
	if !m.accepting {
		m.mu.Unlock()
		return errNotAccepting
	}
	m.inflight++
	m.mu.Unlock()
	defer func() {
		// Model cleanup that has started but cannot yet finish. Tests always
		// release this barrier, including on assertion failure.
		if m.cleanupReady != nil {
			<-m.cleanupReady
		}
		m.mu.Lock()
		defer m.mu.Unlock()
		m.inflight--
		if m.stopping && m.inflight == 0 {
			close(m.drained)
		}
	}()
	if m.accepted != nil {
		close(m.accepted)
	}
	if m.writeReady != nil {
		select {
		case <-m.writeReady:
		case <-ctx.Done():
			return ctx.Err()
		case <-m.ctx.Done():
			return m.ctx.Err()
		}
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if err := m.ctx.Err(); err != nil {
		return err
	}
	_, err := m.file.file.WriteString(text)
	return err
}

func (m *writerModule) Close(ctx context.Context) error {
	m.mu.Lock()
	if !m.stopping {
		m.accepting = false
		m.stopping = true
		if m.cancel != nil {
			m.cancel()
		}
		close(m.stopped)
		if m.inflight == 0 {
			close(m.drained)
		}
	}
	m.mu.Unlock()
	select {
	case <-m.drained:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

func fileSet(t *testing.T) (*plugin.Set, *fileModule, *writerModule) {
	t.Helper()
	file := &fileModule{path: filepath.Join(t.TempDir(), "owned.txt")}
	writer := newWriterModule(file)
	s := newSet(t,
		plugin.Entry{Descriptor: capability.Descriptor{ID: "writer", DependsOn: []capability.ID{"file"}}, Module: writer},
		plugin.Entry{Descriptor: capability.Descriptor{ID: "file"}, Module: file},
	)
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := s.Close(ctx); err != nil {
			t.Errorf("cleanup: %v", err)
		}
	})
	return s, file, writer
}

func barrier(t *testing.T) (chan struct{}, func()) {
	t.Helper()
	ch := make(chan struct{})
	release := sync.OnceFunc(func() { close(ch) })
	t.Cleanup(release)
	return ch, release
}

func assertOpen(t *testing.T, file *fileModule) {
	t.Helper()
	if _, err := file.file.Stat(); err != nil {
		t.Fatalf("dependency closed prematurely: %v", err)
	}
}

func assertClosed(t *testing.T, file *fileModule) {
	t.Helper()
	if _, err := file.file.WriteString("after close"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("write after close = %v, want os.ErrClosed", err)
	}
	if file.opens != 1 || file.closes != 1 {
		t.Fatalf("open/close counts = %d/%d, want 1/1", file.opens, file.closes)
	}
}

func TestResourceConstructionSelectionAndOwnership(t *testing.T) {
	s, file, writer := fileSet(t)
	if _, err := os.Stat(file.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("constructor created a resource: %v", err)
	}
	if err := s.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(file.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("empty selection created a resource: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	defer cancel()
	if err := s.Load(ctx, "writer"); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := writer.Write(t.Context(), "owned"); err != nil {
		t.Fatalf("initialization context canceled module lifetime: %v", err)
	}
	got, err := os.ReadFile(file.path)
	if err != nil || string(got) != "owned" {
		t.Fatalf("read = %q, %v", got, err)
	}
	if err := s.Unload(t.Context(), "writer"); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(t.Context(), "rejected"); !errors.Is(err, errNotAccepting) {
		t.Fatalf("write after unload = %v", err)
	}
	if _, err := file.file.WriteString(" independently"); err != nil {
		t.Fatalf("consumer closed its dependency: %v", err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertClosed(t, file)
}

func TestResourceCloseWaitsForInFlightWork(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, file, writer := fileSet(t)
		writer.accepted = make(chan struct{})
		writer.writeReady, _ = barrier(t)
		var release func()
		writer.cleanupReady, release = barrier(t)
		if err := s.Load(t.Context(), "writer"); err != nil {
			t.Fatal(err)
		}
		written := make(chan error, 1)
		go func() { written <- writer.Write(t.Context(), "canceled") }()
		<-writer.accepted
		unloaded := make(chan error, 1)
		go func() { unloaded <- s.Unload(t.Context(), "writer") }()
		<-writer.stopped
		synctest.Wait()
		select {
		case err := <-unloaded:
			t.Fatalf("unload returned before in-flight cleanup: %v", err)
		default:
		}
		if err := writer.Write(t.Context(), "new"); !errors.Is(err, errNotAccepting) {
			t.Fatalf("admitted work while stopping: %v", err)
		}
		assertOpen(t, file)
		release()
		if err := <-written; !errors.Is(err, context.Canceled) {
			t.Fatalf("write = %v", err)
		}
		if err := <-unloaded; err != nil {
			t.Fatal(err)
		}
		assertOpen(t, file)
		if err := s.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		assertClosed(t, file)
	})
}

func TestResourceDeadlineRetainsDependencyUntilRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, file, writer := fileSet(t)
		writer.accepted = make(chan struct{})
		writer.writeReady, _ = barrier(t)
		var release func()
		writer.cleanupReady, release = barrier(t)
		if err := s.Load(t.Context(), "writer"); err != nil {
			t.Fatal(err)
		}
		written := make(chan error, 1)
		go func() { written <- writer.Write(t.Context(), "canceled") }()
		<-writer.accepted
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		// synctest advances time only after the accepted operation and Close
		// are blocked. This exercises a real context timer without sleeps.
		if err := s.Unload(ctx, "writer"); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("close = %v, want deadline", err)
		}
		if err := s.Unload(t.Context(), "file"); err == nil {
			t.Fatal("released a stopping consumer's dependency")
		}
		if err := writer.Write(t.Context(), "new"); !errors.Is(err, errNotAccepting) {
			t.Fatalf("admitted work after timeout: %v", err)
		}
		assertOpen(t, file)
		release()
		if err := <-written; !errors.Is(err, context.Canceled) {
			t.Fatalf("write = %v", err)
		}
		if err := s.Unload(t.Context(), "writer"); err != nil {
			t.Fatal(err)
		}
		if err := s.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		assertClosed(t, file)
	})
}

func TestResourceRequestCancellationDoesNotStopModule(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, file, writer := fileSet(t)
		writer.accepted = make(chan struct{})
		var release func()
		writer.writeReady, release = barrier(t)
		if err := s.Load(t.Context(), "writer"); err != nil {
			t.Fatal(err)
		}
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		written := make(chan error, 1)
		go func() { written <- writer.Write(ctx, "canceled") }()
		<-writer.accepted
		cancel()
		if err := <-written; !errors.Is(err, context.Canceled) {
			t.Fatalf("write = %v", err)
		}
		writer.accepted = nil
		release()
		if err := writer.Write(t.Context(), "next"); err != nil {
			t.Fatal(err)
		}
		got, err := os.ReadFile(file.path)
		if err != nil || string(got) != "next" {
			t.Fatalf("read = %q, %v", got, err)
		}
	})
}

func TestResourcePartialInitializationRollsBack(t *testing.T) {
	s, file, _ := fileSet(t)
	file.loadErr = errors.New("failed after opening file")
	if err := s.Load(t.Context(), "writer"); !errors.Is(err, file.loadErr) {
		t.Fatalf("load = %v", err)
	}
	assertClosed(t, file)
}

func TestResourceRollbackTimeoutPreservesBothErrors(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, file, _ := fileSet(t)
		file.loadErr = errors.New("failed after opening file")
		var release func()
		file.cleanupReady, release = barrier(t)
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		err := s.Load(ctx, "writer")
		if !errors.Is(err, file.loadErr) || !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("lost load or cleanup error: %v", err)
		}
		assertOpen(t, file)
		if err := s.Load(t.Context(), "writer"); err == nil {
			t.Fatal("loaded with stopping dependency")
		}
		release()
		if err := s.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		assertClosed(t, file)
	})
}

func TestResourceSetsAreIsolated(t *testing.T) {
	a, first, _ := fileSet(t)
	b, second, writer := fileSet(t)
	if err := a.Load(t.Context(), "writer"); err != nil {
		t.Fatal(err)
	}
	if err := b.Load(t.Context(), "writer"); err != nil {
		t.Fatal(err)
	}
	if err := a.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	assertClosed(t, first)
	if err := writer.Write(t.Context(), "still active"); err != nil {
		t.Fatal(err)
	}
	assertOpen(t, second)
}

func TestResourceConcurrentLoadUnloadAndClose(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, file, _ := fileSet(t)
		const callers = 16
		results := make(chan error, callers)
		for range callers {
			go func() { results <- s.Load(t.Context(), "writer") }()
		}
		for range callers {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		if file.opens != 1 {
			t.Fatalf("concurrent loads opened the file %d times", file.opens)
		}
		for i := range callers {
			go func() {
				if i%2 == 0 {
					results <- s.Unload(t.Context(), "writer")
				} else {
					results <- s.Close(t.Context())
				}
			}()
		}
		for range callers {
			if err := <-results; err != nil {
				t.Fatal(err)
			}
		}
		assertClosed(t, file)
	})
}

func TestResourceCanceledCloseWaiterDoesNotSealSet(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, file, writer := fileSet(t)
		writer.accepted = make(chan struct{})
		writer.writeReady, _ = barrier(t)
		var release func()
		writer.cleanupReady, release = barrier(t)
		if err := s.Load(t.Context(), "writer"); err != nil {
			t.Fatal(err)
		}
		written := make(chan error, 1)
		go func() { written <- writer.Write(t.Context(), "canceled") }()
		<-writer.accepted
		unloaded := make(chan error, 1)
		go func() { unloaded <- s.Unload(t.Context(), "writer") }()
		<-writer.stopped
		ctx, cancel := context.WithCancel(t.Context())
		defer cancel()
		closed := make(chan error, 1)
		go func() { closed <- s.Close(ctx) }()
		synctest.Wait() // Close is waiting behind the in-flight unload.
		cancel()
		if err := <-closed; !errors.Is(err, context.Canceled) {
			t.Fatalf("queued close = %v", err)
		}
		release()
		if err := <-written; !errors.Is(err, context.Canceled) {
			t.Fatalf("write = %v", err)
		}
		if err := <-unloaded; err != nil {
			t.Fatal(err)
		}
		if err := s.Load(t.Context(), "file"); err != nil {
			t.Fatalf("canceled Close sealed the set: %v", err)
		}
		assertOpen(t, file)
	})
}
