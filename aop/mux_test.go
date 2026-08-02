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

func TestNamespaceMessageNamesAllowDomainPrefixes(t *testing.T) {
	for _, test := range []struct {
		name string
		want bool
	}{
		{name: "ProtocolMessage", want: true},
		{name: "CommandProtocolMessage", want: true},
		{name: "ReloadProtocolMessage", want: true},
		{name: "Request", want: false},
	} {
		if got := isNamespaceProtocolMessageName(test.name); got != test.want {
			t.Errorf("name %q accepted = %v, want %v", test.name, got, test.want)
		}
	}
}
