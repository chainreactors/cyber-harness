package extension_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chainreactors/aiscan/core/extension"
)

// These fixtures own real resources; all scheduling gates remain test-only.
type fileExtension struct {
	path         string
	file         *os.File
	opens        int
	closes       int
	loadErr      error
	cleanupReady <-chan struct{}
}

func (m *fileExtension) Load(scope *extension.Scope) error {
	ctx := scope.Init()
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

func (m *fileExtension) Close(ctx context.Context) error {
	if m.file == nil {
		return nil
	}
	if m.cleanupReady != nil {
		select {
		case <-m.cleanupReady:
		case <-ctx.Done():
			return errors.Join(extension.ErrCloseIncomplete, ctx.Err())
		}
	}
	if err := m.file.Close(); err != nil {
		return err
	}
	m.closes++
	return nil
}

var errNotAccepting = errors.New("writer is not accepting work")

type writerExtension struct {
	file *fileExtension

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

func newWriterExtension(file *fileExtension) *writerExtension {
	return &writerExtension{file: file, drained: make(chan struct{}), stopped: make(chan struct{})}
}

func (m *writerExtension) Load(scope *extension.Scope) error {
	ctx := scope.Init()
	if _, err := m.file.file.Stat(); err != nil {
		return err
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	// Initialization cancellation is distinct from the extension's lifetime.
	m.ctx, m.cancel = context.WithCancel(context.WithoutCancel(ctx))
	m.accepting = true
	return nil
}

func (m *writerExtension) Write(ctx context.Context, text string) error {
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

func (m *writerExtension) Close(ctx context.Context) error {
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
		return errors.Join(extension.ErrCloseIncomplete, ctx.Err())
	}
}

func fileSet(t *testing.T) (*extension.Set, *fileExtension, *writerExtension) {
	t.Helper()
	file := &fileExtension{path: filepath.Join(t.TempDir(), "owned.txt")}
	writer := newWriterExtension(file)
	s := newSet(t,
		extension.Entry{ID: "writer", DependsOn: []string{"file"}, Extension: writer},
		extension.Entry{ID: "file", Extension: file},
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

func assertOpen(t *testing.T, file *fileExtension) {
	t.Helper()
	if _, err := file.file.Stat(); err != nil {
		t.Fatalf("dependency closed prematurely: %v", err)
	}
}

func assertClosed(t *testing.T, file *fileExtension) {
	t.Helper()
	if _, err := file.file.WriteString("after close"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("write after close = %v, want os.ErrClosed", err)
	}
	if file.opens != 1 || file.closes != 1 {
		t.Fatalf("open/close counts = %d/%d, want 1/1", file.opens, file.closes)
	}
}

func TestResourceConstructionAndOwnership(t *testing.T) {
	s, file, writer := fileSet(t)
	if _, err := os.Stat(file.path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("constructor created a resource: %v", err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	if err := s.Load(ctx); err != nil {
		t.Fatal(err)
	}
	cancel()
	if err := writer.Write(t.Context(), "owned"); err != nil {
		t.Fatalf("initialization context canceled extension lifetime: %v", err)
	}
	got, err := os.ReadFile(file.path)
	if err != nil || string(got) != "owned" {
		t.Fatalf("read = %q, %v", got, err)
	}
	if err := s.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := writer.Write(t.Context(), "rejected"); !errors.Is(err, errNotAccepting) {
		t.Fatalf("write after close = %v", err)
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
		if err := s.Load(t.Context()); err != nil {
			t.Fatal(err)
		}
		written := make(chan error, 1)
		go func() { written <- writer.Write(t.Context(), "canceled") }()
		<-writer.accepted
		closed := make(chan error, 1)
		go func() { closed <- s.Close(t.Context()) }()
		<-writer.stopped
		synctest.Wait()
		select {
		case err := <-closed:
			t.Fatalf("close returned before in-flight cleanup: %v", err)
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
		if err := <-closed; err != nil {
			t.Fatal(err)
		}
		assertClosed(t, file)
	})
}

func TestResourceCloseDeadlineRetainsDependencyUntilRetry(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		s, file, writer := fileSet(t)
		writer.accepted = make(chan struct{})
		writer.writeReady, _ = barrier(t)
		var release func()
		writer.cleanupReady, release = barrier(t)
		if err := s.Load(t.Context()); err != nil {
			t.Fatal(err)
		}
		written := make(chan error, 1)
		go func() { written <- writer.Write(t.Context(), "canceled") }()
		<-writer.accepted
		ctx, cancel := context.WithTimeout(t.Context(), 10*time.Second)
		defer cancel()
		if err := s.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("close = %v, want deadline", err)
		}
		assertOpen(t, file)
		release()
		if err := <-written; !errors.Is(err, context.Canceled) {
			t.Fatalf("write = %v", err)
		}
		if err := s.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		assertClosed(t, file)
	})
}

func TestResourceRequestCancellationDoesNotStopExtension(t *testing.T) {
	s, file, writer := fileSet(t)
	writer.accepted = make(chan struct{})
	var release func()
	writer.writeReady, release = barrier(t)
	if err := s.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(t.Context())
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
}

func TestResourcePartialInitializationRollsBack(t *testing.T) {
	s, file, _ := fileSet(t)
	file.loadErr = errors.New("failed after opening file")
	if err := s.Load(t.Context()); !errors.Is(err, file.loadErr) {
		t.Fatalf("load = %v", err)
	}
	assertClosed(t, file)
}

func TestResourceSetsAreIsolated(t *testing.T) {
	a, first, _ := fileSet(t)
	b, second, writer := fileSet(t)
	if err := a.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := b.Load(t.Context()); err != nil {
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
