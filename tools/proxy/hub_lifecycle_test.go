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

func TestHubShutdownReportsStorageFailureOnce(t *testing.T) {
	for _, throughInfra := range []bool{false, true} {
		name := "hub"
		if throughInfra {
			name = "infra"
		}
		t.Run(name, func(t *testing.T) {
			store := NewFlowStore(8)
			if err := store.SetBodyDir(t.TempDir()); err != nil {
				t.Fatal(err)
			}
			hub := NewProxyHub(NewState(""), store, t.TempDir(), false, nil)
			closeResource := hub.Close
			if throughInfra {
				infra := hub
				closeResource = infra.Close
			}
			t.Cleanup(func() { _ = closeResource(context.Background()) })
			// A real closed journal makes the final Sync/Close fail.
			if err := store.indexFile.Close(); err != nil {
				t.Fatal(err)
			}
			err := closeResource(t.Context())
			if !errors.Is(err, os.ErrClosed) {
				t.Fatalf("completed storage cleanup = %v", err)
			}
			if store.indexFile != nil || store.indexSub != nil {
				t.Fatal("completed cleanup retained journal ownership")
			}
			if err := closeResource(t.Context()); err != nil {
				t.Fatalf("terminal error repeated: %v", err)
			}
		})
	}
}

func TestHubShutdownTimeoutRetainsDrainAndFinalError(t *testing.T) {
	dir := t.TempDir()
	synctest.Test(t, func(t *testing.T) {
		store := NewFlowStore(8)
		if err := store.SetBodyDir(dir); err != nil {
			t.Fatal(err)
		}
		if err := store.indexSub.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		entered, release := make(chan struct{}), make(chan struct{})
		unblock := sync.OnceFunc(func() { close(release) })
		want := errors.New("journal callback failed")
		var err error
		store.indexSub, err = store.events.SubscribeAsync(eventbus.SubscribeOptions[Flow]{}, func(Flow) error {
			close(entered)
			<-release
			return want
		})
		if err != nil {
			t.Fatal(err)
		}
		hub := NewProxyHub(NewState(""), store, dir, false, nil)
		defer func() { unblock(); _ = hub.Close(context.Background()) }()
		file := store.indexFile
		store.Add(Flow{})
		<-entered
		ctx, cancel := context.WithTimeout(t.Context(), time.Second)
		defer cancel()
		err = hub.Close(ctx)
		if !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Shutdown with active callback = %v", err)
		}
		if _, err := file.Stat(); err != nil {
			t.Fatalf("journal closed before callback finished: %v", err)
		}
		unblock()
		err = hub.Close(t.Context())
		if !errors.Is(err, want) {
			t.Fatalf("Shutdown after drain = %v", err)
		}
		if _, err := file.WriteString("after shutdown"); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("journal remains open: %v", err)
		}
		if err := hub.Close(t.Context()); err != nil {
			t.Fatalf("terminal error repeated: %v", err)
		}
	})
}
