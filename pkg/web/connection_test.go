package web

import (
	"context"
	"errors"
	"io"
	"sync"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
)

type connectionTestStream struct {
	recvCh  chan *aop.Envelope
	recvErr chan error

	mu      sync.Mutex
	sent    []*aop.Envelope
	sendErr error
}

type blockingConnectionTestStream struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func (s *blockingConnectionTestStream) Recv() (*aop.Envelope, error) {
	return nil, io.EOF
}

func (s *blockingConnectionTestStream) Send(*aop.Envelope) error {
	s.once.Do(func() { close(s.started) })
	<-s.release
	return nil
}

func newConnectionTestStream() *connectionTestStream {
	return &connectionTestStream{recvCh: make(chan *aop.Envelope), recvErr: make(chan error, 1)}
}

func (s *connectionTestStream) Recv() (*aop.Envelope, error) {
	select {
	case envelope := <-s.recvCh:
		return envelope, nil
	case err := <-s.recvErr:
		return nil, err
	}
}

func (s *connectionTestStream) Send(envelope *aop.Envelope) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sendErr != nil {
		return s.sendErr
	}
	s.sent = append(s.sent, envelope)
	return nil
}

func TestConnectionDispatchesFirstAndReceivedEnvelopes(t *testing.T) {
	stream := newConnectionTestStream()
	connection, err := NewConnection(context.Background(), stream)
	if err != nil {
		t.Fatal(err)
	}
	dispatched := make(chan string, 2)
	done := make(chan error, 1)
	go func() {
		done <- connection.Run(&aop.Envelope{Id: "first"}, func(_ context.Context, envelope *aop.Envelope, _ aop.SendFunc) error {
			dispatched <- envelope.Id
			return nil
		})
	}()
	stream.recvCh <- &aop.Envelope{Id: "second"}
	stream.recvErr <- io.EOF
	if got := <-dispatched; got != "first" {
		t.Fatalf("first dispatch = %q", got)
	}
	if got := <-dispatched; got != "second" {
		t.Fatalf("second dispatch = %q", got)
	}
	if err := <-done; !errors.Is(err, io.EOF) {
		t.Fatalf("Run() error = %v", err)
	}
}

func TestConnectionSerializesConcurrentSends(t *testing.T) {
	stream := newConnectionTestStream()
	connection, err := NewConnection(context.Background(), stream)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	const count = 32
	var wg sync.WaitGroup
	for i := 0; i < count; i++ {
		wg.Add(1)
		go func(id string) {
			defer wg.Done()
			if err := connection.Send(&aop.Envelope{Id: id}); err != nil {
				t.Errorf("Send() error = %v", err)
			}
		}(string(rune('a' + i)))
	}
	wg.Wait()
	stream.mu.Lock()
	defer stream.mu.Unlock()
	if len(stream.sent) != count {
		t.Fatalf("sent %d envelopes, want %d", len(stream.sent), count)
	}
	seen := make(map[string]bool, count)
	for _, envelope := range stream.sent {
		if seen[envelope.Id] {
			t.Fatalf("duplicate envelope %q", envelope.Id)
		}
		seen[envelope.Id] = true
	}
}

func TestConnectionPreservesFIFOSendOrder(t *testing.T) {
	stream := newConnectionTestStream()
	connection, err := NewConnection(context.Background(), stream)
	if err != nil {
		t.Fatal(err)
	}
	defer connection.Close()
	for _, id := range []string{"one", "two", "three"} {
		if err := connection.Send(&aop.Envelope{Id: id}); err != nil {
			t.Fatal(err)
		}
	}
	stream.mu.Lock()
	defer stream.mu.Unlock()
	for index, want := range []string{"one", "two", "three"} {
		if got := stream.sent[index].GetId(); got != want {
			t.Fatalf("send[%d] = %q, want %q", index, got, want)
		}
	}
}

func TestConnectionSendFailureConverges(t *testing.T) {
	want := errors.New("write failed")
	stream := newConnectionTestStream()
	stream.sendErr = want
	connection, err := NewConnection(context.Background(), stream)
	if err != nil {
		t.Fatal(err)
	}
	if err := connection.Send(&aop.Envelope{Id: "one"}); !errors.Is(err, want) {
		t.Fatalf("Send() error = %v", err)
	}
	if err := connection.Send(&aop.Envelope{Id: "two"}); !errors.Is(err, want) {
		t.Fatalf("second Send() error = %v", err)
	}
}

func TestConnectionHandlerFailureConverges(t *testing.T) {
	want := errors.New("handler failed")
	stream := newConnectionTestStream()
	connection, err := NewConnection(context.Background(), stream)
	if err != nil {
		t.Fatal(err)
	}
	err = connection.Run(&aop.Envelope{Id: "first"}, func(context.Context, *aop.Envelope, aop.SendFunc) error { return want })
	if !errors.Is(err, want) {
		t.Fatalf("Run() error = %v", err)
	}
	if err := connection.Send(&aop.Envelope{Id: "after"}); !errors.Is(err, want) {
		t.Fatalf("Send() after handler error = %v", err)
	}
}

func TestConnectionContextCancellation(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	stream := newConnectionTestStream()
	connection, err := NewConnection(ctx, stream)
	if err != nil {
		t.Fatal(err)
	}
	done := make(chan error, 1)
	go func() {
		done <- connection.Run(nil, func(context.Context, *aop.Envelope, aop.SendFunc) error { return nil })
	}()
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Run() error = %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("connection did not stop after cancellation")
	}
}

func TestConnectionCloseWaitsForActiveWriter(t *testing.T) {
	stream := &blockingConnectionTestStream{started: make(chan struct{}), release: make(chan struct{})}
	connection, err := NewConnection(context.Background(), stream)
	if err != nil {
		t.Fatal(err)
	}
	sendDone := make(chan error, 1)
	go func() { sendDone <- connection.Send(&aop.Envelope{Id: "one"}) }()
	select {
	case <-stream.started:
	case <-time.After(time.Second):
		t.Fatal("writer did not start")
	}
	closeDone := make(chan struct{})
	go func() {
		connection.Close()
		close(closeDone)
	}()
	select {
	case <-closeDone:
		t.Fatal("Close returned while the stream writer was active")
	case <-time.After(20 * time.Millisecond):
	}
	close(stream.release)
	select {
	case <-closeDone:
	case <-time.After(time.Second):
		t.Fatal("Close did not return after the stream writer stopped")
	}
	if err := <-sendDone; err != nil && !errors.Is(err, ErrConnectionClosed) {
		t.Fatalf("Send() error = %v", err)
	}
}
