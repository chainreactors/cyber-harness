package host

import (
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"sync"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

func testHost(t *testing.T, ctx context.Context, mux *aop.NamespaceMux) *Host {
	t.Helper()
	h := New(ctx, mux)
	t.Cleanup(h.Close)
	return h
}

func testMux(t *testing.T, handler aop.NamespaceHandler) *aop.NamespaceMux {
	t.Helper()
	mux := aop.NewNamespaceMux()
	if err := mux.Register(&aop.ProtocolMessage{}, handler); err != nil {
		t.Fatal(err)
	}
	return mux
}

func TestInlineAndStdioUseSameDispatch(t *testing.T) {
	mux := testMux(t, func(_ context.Context, e *aop.Envelope, message proto.Message, send aop.SendFunc) error {
		return send(aop.Reply(e.Id, message))
	})
	request := aop.MustWrap("request", "", aop.NewProtocolError("EXAMPLE", "example message"))
	var inline *aop.Envelope
	if err := testHost(t, context.Background(), mux).Handle(request, func(e *aop.Envelope) error { inline = e; return nil }); err != nil {
		t.Fatal(err)
	}
	data, err := protojson.Marshal(request)
	if err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := testHost(t, context.Background(), mux).Serve(NewStdio(bytes.NewReader(data), &output)); err != nil {
		t.Fatal(err)
	}
	stdio, err := NewStdio(&output, io.Discard).Recv()
	if err != nil {
		t.Fatal(err)
	}
	if inline.ReplyTo != request.Id || stdio.ReplyTo != request.Id || !proto.Equal(inline.Payload, stdio.Payload) {
		t.Fatalf("different response: inline=%v stdio=%v", inline, stdio)
	}
}
func TestAsyncRepliesRemainAvailableAfterDispatch(t *testing.T) {
	for _, stdio := range []bool{false, true} {
		release := make(chan struct{})
		var pending sync.WaitGroup
		mux := testMux(t, func(_ context.Context, e *aop.Envelope, _ proto.Message, send aop.SendFunc) error {
			pending.Add(1)
			go func() {
				defer pending.Done()
				<-release
				_ = send(aop.Reply(e.Id, aop.NewProtocolError("LATE", "asynchronous response")))
			}()
			return nil
		})
		request := aop.MustWrap("async", "", &aop.ProtocolMessage{})
		var output bytes.Buffer
		data, err := protojson.Marshal(request)
		if err != nil {
			t.Fatal(err)
		}
		stream := NewStdio(bytes.NewReader(data), &output)
		h := testHost(t, context.Background(), mux)
		if stdio {
			err = h.Serve(stream)
		} else {
			err = h.Handle(request, stream.Send)
		}
		close(release)
		pending.Wait()
		if err != nil || h.Err() != nil {
			t.Fatalf("dispatch=%v write=%v", err, h.Err())
		}
		response, err := NewStdio(&output, io.Discard).Recv()
		if err != nil || response.GetReplyTo() != "async" {
			t.Fatalf("lost asynchronous response: response=%v err=%v", response, err)
		}
	}
}

func TestSendFailureIsNotAProtocolError(t *testing.T) {
	want := errors.New("connection lost")
	mux := testMux(t, func(_ context.Context, e *aop.Envelope, message proto.Message, send aop.SendFunc) error {
		return send(aop.Reply(e.Id, message))
	})
	calls := 0
	err := testHost(t, context.Background(), mux).Handle(aop.MustWrap("request", "", &aop.ProtocolMessage{}), func(*aop.Envelope) error {
		calls++
		return want
	})
	if !errors.Is(err, want) || calls != 1 {
		t.Fatalf("write failure was retried or lost: err=%v writes=%d", err, calls)
	}
}

func TestProtocolErrorsMatchAcrossInlineAndStdio(t *testing.T) {
	for _, code := range []string{"UNSUPPORTED_MESSAGE", "INVALID_PAYLOAD"} {
		t.Run(code, func(t *testing.T) {
			mux := aop.NewNamespaceMux()
			if code == "INVALID_PAYLOAD" {
				mux = testMux(t, func(context.Context, *aop.Envelope, proto.Message, aop.SendFunc) error {
					return errors.New("unsupported core message")
				})
			}
			request := aop.MustWrap("request", "", &aop.ProtocolMessage{})
			var inline *aop.Envelope
			if err := testHost(t, context.Background(), mux).Handle(request, func(e *aop.Envelope) error { inline = e; return nil }); err != nil {
				t.Fatal(err)
			}
			data, err := protojson.Marshal(request)
			if err != nil {
				t.Fatal(err)
			}
			var output bytes.Buffer
			if err := testHost(t, context.Background(), mux).Serve(NewStdio(bytes.NewReader(data), &output)); err != nil {
				t.Fatal(err)
			}
			stdio, err := NewStdio(&output, io.Discard).Recv()
			if err != nil {
				t.Fatal(err)
			}
			message, err := aop.Unwrap(inline)
			if err != nil {
				t.Fatal(err)
			}
			if message.(*aop.ProtocolMessage).GetProtocolError().GetCode() != code || !proto.Equal(inline.Payload, stdio.Payload) || stdio.ReplyTo != request.Id {
				t.Fatalf("different error response: inline=%v stdio=%v", inline, stdio)
			}
		})
	}
}

func TestServeReturnsResponseWriteFailure(t *testing.T) {
	mux := testMux(t, func(_ context.Context, e *aop.Envelope, message proto.Message, send aop.SendFunc) error {
		return send(aop.Reply(e.Id, message))
	})
	data, err := protojson.Marshal(aop.MustWrap("request", "", &aop.ProtocolMessage{}))
	if err != nil {
		t.Fatal(err)
	}
	err = testHost(t, context.Background(), mux).Serve(NewStdio(bytes.NewReader(data), new(shortWriter)))
	if !errors.Is(err, io.ErrShortWrite) {
		t.Fatalf("write failure was lost: %v", err)
	}
}

func TestCancellationPreventsDispatch(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	mux := testMux(t, func(context.Context, *aop.Envelope, proto.Message, aop.SendFunc) error {
		t.Fatal("cancelled request dispatched")
		return nil
	})
	request := aop.MustWrap("cancelled", "", &aop.ProtocolMessage{})
	if err := testHost(t, ctx, mux).Handle(request, func(*aop.Envelope) error { t.Fatal("unexpected send"); return nil }); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
	if err := testHost(t, ctx, mux).Serve(NewStdio(strings.NewReader(""), io.Discard)); !errors.Is(err, context.Canceled) {
		t.Fatal(err)
	}
}
