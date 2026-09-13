package extension_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"testing/synctest"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/extension"
)

// outputConsumer is a test-only consumer of an actual borrowed file. Callback
// admission and completion belong entirely to Subscription, with no second
// inflight counter or completion channel in the extension.
type outputConsumer struct {
	file         *fileExtension
	bus          *eventbus.Bus[string]
	subscription *eventbus.Subscription[string]
	err          error
	entered      chan struct{}
	release      chan struct{}
	asyncError   error
}

func (m *outputConsumer) Load(*extension.Scope) error {
	if m.asyncError != nil {
		var err error
		m.subscription, err = m.bus.SubscribeAsync(eventbus.SubscribeOptions[string]{}, func(value string) error {
			_, err := m.file.file.WriteString(value)
			return errors.Join(err, m.asyncError)
		})
		return err
	}
	m.subscription = m.bus.Subscribe(func(value string) {
		close(m.entered)
		<-m.release
		_, m.err = m.file.file.WriteString(value)
	})
	return nil
}

func (m *outputConsumer) Close(ctx context.Context) error {
	if err := m.subscription.Close(ctx); err != nil {
		return errors.Join(extension.ErrCloseIncomplete, err)
	}
	return errors.Join(m.err, m.subscription.Err())
}

func TestAsyncProcessingFailureDoesNotRetainBorrowedExtensionResource(t *testing.T) {
	file := &fileExtension{path: filepath.Join(t.TempDir(), "events.txt")}
	want := errors.New("processing failed after writing")
	bus := eventbus.New[string]()
	output := &outputConsumer{file: file, bus: bus, asyncError: want}
	set := newSet(t,
		extension.Entry{ID: "output", DependsOn: []string{"file"}, Extension: output},
		extension.Entry{ID: "file", Extension: file},
	)
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	bus.Emit("accepted")
	if err := set.Close(t.Context()); !errors.Is(err, want) || errors.Is(err, extension.ErrCloseIncomplete) {
		t.Fatalf("Close = %v", err)
	}
	assertClosed(t, file)
	data, err := os.ReadFile(file.path)
	if err != nil || string(data) != "accepted" {
		t.Fatalf("output = %q, %v", data, err)
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatalf("retry repeated terminal error: %v", err)
	}
}

func TestSubscriptionTimeoutRetainsBorrowedExtensionResource(t *testing.T) {
	path := filepath.Join(t.TempDir(), "events.txt")
	synctest.Test(t, func(t *testing.T) {
		bus := eventbus.New[string]()
		file := &fileExtension{path: path}
		output := &outputConsumer{file: file, bus: bus, entered: make(chan struct{}), release: make(chan struct{})}
		set := newSet(t,
			extension.Entry{ID: "output", DependsOn: []string{"file"}, Extension: output},
			extension.Entry{ID: "file", Extension: file},
		)
		defer func() {
			close(output.release)
			if err := set.Close(context.Background()); err != nil {
				t.Error(err)
			}
		}()
		if err := set.Load(context.Background()); err != nil {
			t.Fatal(err)
		}
		go bus.Emit("accepted")
		<-output.entered
		ctx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		if err := set.Close(ctx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("Close = %v", err)
		}
		if _, err := file.file.Stat(); err != nil {
			t.Fatalf("dependency released before callback completed: %v", err)
		}
		bus.Emit("rejected")
		output.release <- struct{}{}
		if err := set.Close(context.Background()); err != nil {
			t.Fatal(err)
		}
		if _, err := file.file.Stat(); err == nil {
			t.Fatal("file is still open after successful Close")
		}
		if file.opens != 1 || file.closes != 1 {
			t.Fatalf("opens=%d closes=%d", file.opens, file.closes)
		}
	})
	data, err := os.ReadFile(path)
	if err != nil || string(data) != "accepted" {
		t.Fatalf("output = %q, error = %v", data, err)
	}
}
