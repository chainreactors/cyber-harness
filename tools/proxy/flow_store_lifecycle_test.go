package proxy

import (
	"context"
	"errors"
	"os"
	"sync"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
)

func TestStoreCloseTimeoutKeepsMetadataIndexUntilRetry(t *testing.T) {
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		s := NewFlowStore(8)
		if err := s.SetBodyDir(dir); err != nil {
			t.Fatal(err)
		}
		if err := s.indexSub.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		defer func() { unblock(); _ = s.Close() }()
		var err error
		s.indexSub, err = s.events.SubscribeAsync(eventbus.SubscribeOptions[Flow]{}, func(flow Flow) error {
			close(entered)
			<-release
			return s.appendIndex(flow)
		})
		if err != nil {
			t.Fatal(err)
		}
		file, sub := s.indexFile, s.indexSub
		s.Add(Flow{})
		<-entered
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		err = s.closeContext(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close = %v", err)
		}
		if _, err := file.Stat(); err != nil {
			t.Fatalf("metadata index released before callback finished: %v", err)
		}
		if s.indexSub != sub {
			t.Fatal("timeout lost subscription ownership")
		}
		unblock()
		<-sub.Done()
		// Callback completion does not delegate file release to a hidden worker.
		if _, err := file.Stat(); err != nil {
			t.Fatalf("metadata index closed without retry: %v", err)
		}
		// The expired waiting context is irrelevant once the drain is complete.
		if err := s.closeContext(ctx); err != nil {
			t.Fatal(err)
		}
		if _, err := file.WriteString("after close"); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("metadata index remains open after retry: %v", err)
		}
	})
}

func TestStoreCloseReportsProcessingFailureOnceAndReleasesFile(t *testing.T) {
	s := NewFlowStore(8)
	if err := s.SetBodyDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	if err := s.indexSub.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	want := errors.New("metadata index processing failed")
	var err error
	s.indexSub, err = s.events.SubscribeAsync(eventbus.SubscribeOptions[Flow]{}, func(Flow) error { return want })
	if err != nil {
		t.Fatal(err)
	}
	file := s.indexFile
	s.Add(Flow{})
	if err := s.Close(); !errors.Is(err, want) {
		t.Fatalf("Close = %v", err)
	}
	if _, err := file.WriteString("after close"); !errors.Is(err, os.ErrClosed) {
		t.Fatalf("metadata index not released: %v", err)
	}
	if err := s.Close(); err != nil {
		t.Fatalf("terminal error repeated: %v", err)
	}
	if err := s.IndexError(); !errors.Is(err, want) {
		t.Fatalf("processing diagnostic lost: %v", err)
	}
}
