package aop

import (
	"context"
	"testing"

	filepb "github.com/chainreactors/aiscan/aop/file"
	"google.golang.org/protobuf/proto"
)

func TestNamespaceMuxRegistersAndDispatches(t *testing.T) {
	mux := NewNamespaceMux()
	called := false
	if err := mux.Register(&filepb.ProtocolMessage{}, func(_ context.Context, _ *Envelope, message proto.Message, _ SendFunc) error {
		called = message.(*filepb.ProtocolMessage).GetReadRequest().GetPath() == "/tmp/x"
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	envelope := MustWrap("id", "", &filepb.ProtocolMessage{Message: &filepb.ProtocolMessage_ReadRequest{ReadRequest: &filepb.ReadRequest{Path: "/tmp/x"}}})
	handled, err := mux.Dispatch(context.Background(), envelope, nil)
	if err != nil || !handled || !called {
		t.Fatalf("handled=%v called=%v err=%v", handled, called, err)
	}
}

func TestNamespaceMuxRejectsDuplicate(t *testing.T) {
	mux := NewNamespaceMux()
	handler := func(context.Context, *Envelope, proto.Message, SendFunc) error { return nil }
	if err := mux.Register(&filepb.ProtocolMessage{}, handler); err != nil {
		t.Fatal(err)
	}
	if err := mux.Register(&filepb.ProtocolMessage{}, handler); err == nil {
		t.Fatal("duplicate namespace registration succeeded")
	}
}
