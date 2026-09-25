package host

import (
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	"google.golang.org/protobuf/proto"
)

func TestCloseCancelsAndWaitsForDispatch(t *testing.T) {
	entered, canceled, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	mux := testMux(t, func(ctx context.Context, _ *aop.Envelope, _ proto.Message, _ aop.SendFunc) error {
		close(entered)
		<-ctx.Done()
		close(canceled)
		<-release
		return ctx.Err()
	})
	h := New(mux)
	request := aop.MustWrap("request", "", &aop.ProtocolMessage{})
	dispatched := make(chan error, 1)
	go func() { dispatched <- h.Handle(request, func(*aop.Envelope) error { return nil }) }()
	<-entered
	closed := make(chan struct{})
	go func() { h.Close(); close(closed) }()
	select {
	case <-canceled:
	case <-time.After(5 * time.Second):
		t.Fatal("Close did not cancel dispatch")
	}
	select {
	case <-closed:
		t.Fatal("Close returned before dispatch finished")
	default:
	}
	if err := h.Handle(request, func(*aop.Envelope) error { t.Error("sent after Close"); return nil }); !errors.Is(err, context.Canceled) {
		t.Errorf("admission after Close: %v", err)
	}
	close(release)
	if err := <-dispatched; !errors.Is(err, context.Canceled) {
		t.Error(err)
	}
	<-closed
	h.Close()
}

func TestClosedHostRejectsLateReplyWithoutClosingAnotherConnection(t *testing.T) {
	var retained aop.SendFunc
	handler := func(_ context.Context, _ *aop.Envelope, _ proto.Message, send aop.SendFunc) error {
		retained = send
		return nil
	}
	first := New(testMux(t, handler))
	second := New(testMux(t, handler))
	defer second.Close()
	request := aop.MustWrap("request", "", &aop.ProtocolMessage{})
	writes := 0
	send := func(*aop.Envelope) error { writes++; return nil }
	if err := first.Handle(request, send); err != nil {
		t.Fatal(err)
	}
	first.Close()
	if err := retained(aop.Reply(request.Id, &aop.ProtocolMessage{})); !errors.Is(err, context.Canceled) || writes != 0 {
		t.Fatalf("late reply: err=%v writes=%d", err, writes)
	}
	if err := second.Handle(request, send); err != nil {
		t.Fatalf("another connection was closed: %v", err)
	}
	if err := retained(aop.Reply(request.Id, &aop.ProtocolMessage{})); err != nil || writes != 1 {
		t.Fatalf("second Host cannot send: err=%v writes=%d", err, writes)
	}
}

func TestLateWriteFailureSurvivesEOF(t *testing.T) {
	h := New(aop.NewNamespaceMux(t.Context()))
	defer h.Close()
	if err := h.Serve(NewStdio(strings.NewReader(""), io.Discard)); err != nil {
		t.Fatal(err)
	}
	want := errors.New("late write failure")
	request := aop.Reply("request", &aop.ProtocolMessage{})
	if err := h.Send(request, func(*aop.Envelope) error { return want }); !errors.Is(err, want) {
		t.Fatal(err)
	}
	if !errors.Is(h.Err(), want) || !errors.Is(h.Context().Err(), context.Canceled) {
		t.Fatalf("error=%v context=%v", h.Err(), h.Context().Err())
	}
	if err := h.Send(request, func(*aop.Envelope) error { t.Error("failed sender called again"); return nil }); !errors.Is(err, want) {
		t.Fatal(err)
	}
}

func TestConcurrentCloseAndAdmission(t *testing.T) {
	mux := testMux(t, func(_ context.Context, request *aop.Envelope, message proto.Message, send aop.SendFunc) error {
		return send(aop.Reply(request.Id, message))
	})
	h := New(mux)
	request := aop.MustWrap("request", "", &aop.ProtocolMessage{})
	var workers sync.WaitGroup
	for i := 0; i < 40; i++ {
		workers.Add(1)
		go func(i int) {
			defer workers.Done()
			if i%4 == 0 {
				h.Close()
				return
			}
			err := h.Handle(request, func(*aop.Envelope) error { return nil })
			if err != nil && !errors.Is(err, context.Canceled) {
				t.Errorf("dispatch: %v", err)
			}
		}(i)
	}
	workers.Wait()
}

func TestStreamOwnerCanInterruptBlockedRead(t *testing.T) {
	reader, writer := io.Pipe()
	defer reader.Close()
	defer writer.Close()
	h := New(aop.NewNamespaceMux(t.Context()))
	// The embedding owns this pipe, so it may close it on communication cancel.
	stop := context.AfterFunc(h.Context(), func() { _ = writer.CloseWithError(context.Canceled) })
	defer stop()
	done := make(chan error, 1)
	go func() { done <- h.Serve(NewStdio(reader, io.Discard)) }()
	if _, err := writer.Write([]byte(" ")); err != nil {
		t.Fatal(err)
	}
	h.Close()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatal(err)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("read did not stop after owner closed pipe")
	}
}
