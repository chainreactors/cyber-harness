package aop

import (
	"context"
	"errors"
	"sync"
	"testing"

	filepb "github.com/chainreactors/cyber/aop/file"
	"google.golang.org/protobuf/proto"
)

func TestNamespaceCloseCancelsAndDrains(t *testing.T) {
	mux := NewNamespaceMux(t.Context())
	entered, release := make(chan struct{}), make(chan struct{})
	var once sync.Once
	t.Cleanup(func() { once.Do(func() { close(release) }); _ = mux.Close(context.Background()) })
	var handlerCtx context.Context
	if err := mux.Register(&filepb.ProtocolMessage{}, func(ctx context.Context, _ *Envelope, _ proto.Message, _ SendFunc) error {
		handlerCtx = ctx
		close(entered)
		<-release
		return ctx.Err()
	}); err != nil {
		t.Fatal(err)
	}
	request := MustWrap("file", "", &filepb.ProtocolMessage{})
	finished := make(chan error, 1)
	go func() { _, err := mux.Dispatch(request, nil); finished <- err }()
	<-entered
	expired, cancel := context.WithCancel(t.Context())
	cancel()
	if err := mux.Close(expired); !errors.Is(err, context.Canceled) {
		t.Fatalf("close while busy: %v", err)
	}
	if !errors.Is(handlerCtx.Err(), context.Canceled) {
		t.Fatal("accepted handler was not cancelled")
	}
	if handled, err := mux.Dispatch(request, nil); handled || !errors.Is(err, ErrNamespaceUnavailable) {
		t.Fatalf("admission after close: handled=%v err=%v", handled, err)
	}
	once.Do(func() { close(release) })
	if err := <-finished; !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := mux.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if err := mux.Register(&ProtocolMessage{}, func(context.Context, *Envelope, proto.Message, SendFunc) error { return nil }); !errors.Is(err, ErrNamespaceUnavailable) {
		t.Fatalf("closed mux was reused: %v", err)
	}
}

func TestNamespaceContextLivesUntilConnectionCloses(t *testing.T) {
	parent, cancel := context.WithCancel(t.Context())
	defer cancel()
	mux := NewNamespaceMux(parent)
	defer mux.Close(context.Background())
	var fileCtx, coreCtx context.Context
	if err := mux.Register(&filepb.ProtocolMessage{}, func(ctx context.Context, _ *Envelope, _ proto.Message, _ SendFunc) error { fileCtx = ctx; return nil }); err != nil {
		t.Fatal(err)
	}
	if err := mux.Register(&ProtocolMessage{}, func(ctx context.Context, _ *Envelope, _ proto.Message, _ SendFunc) error { coreCtx = ctx; return nil }); err != nil {
		t.Fatal(err)
	}
	for _, message := range []proto.Message{&filepb.ProtocolMessage{}, &ProtocolMessage{}} {
		if handled, err := mux.Dispatch(MustWrap("id", "", message), nil); !handled || err != nil {
			t.Fatalf("dispatch: %v %v", handled, err)
		}
	}
	if fileCtx.Err() != nil || coreCtx.Err() != nil {
		t.Fatal("returning from dispatch cancelled a subscription lifetime")
	}
	cancel()
	if fileCtx.Err() == nil || coreCtx.Err() == nil {
		t.Fatal("connection shutdown did not cancel namespace lifetime")
	}
}

func TestNamespaceFailedRegistrationDoesNotMutateMux(t *testing.T) {
	mux := NewNamespaceMux(t.Context())
	defer mux.Close(context.Background())
	handler := func(context.Context, *Envelope, proto.Message, SendFunc) error { return nil }
	if err := mux.Register(&filepb.ProtocolMessage{}, handler); err != nil {
		t.Fatal(err)
	}
	if err := mux.Register(&filepb.ProtocolMessage{}, handler); err == nil {
		t.Fatal("duplicate registration succeeded")
	}
	if _, err := mux.Dispatch(MustWrap("id", "", &filepb.ProtocolMessage{}), nil); err != nil {
		t.Fatal(err)
	}
	var typedNil *ProtocolMessage
	if err := mux.Register(typedNil, handler); err == nil {
		t.Fatal("nil prototype was accepted")
	}
}

func TestNamespaceCloseRacesWithDispatch(t *testing.T) {
	mux := NewNamespaceMux(t.Context())
	if err := mux.Register(&filepb.ProtocolMessage{}, func(context.Context, *Envelope, proto.Message, SendFunc) error { return nil }); err != nil {
		t.Fatal(err)
	}
	request := MustWrap("id", "", &filepb.ProtocolMessage{})
	var workers sync.WaitGroup
	for i := range 64 {
		workers.Go(func() {
			if i%4 == 0 {
				if err := mux.Close(t.Context()); err != nil {
					t.Error(err)
				}
				return
			}
			_, err := mux.Dispatch(request, nil)
			if err != nil && !errors.Is(err, ErrNamespaceUnavailable) && !errors.Is(err, context.Canceled) {
				t.Error(err)
			}
		})
	}
	workers.Wait()
}
