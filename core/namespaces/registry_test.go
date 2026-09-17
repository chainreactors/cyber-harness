package namespaces

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/aop"
	execpb "github.com/chainreactors/cyber/aop/exec"
	filepb "github.com/chainreactors/cyber/aop/file"
	"google.golang.org/protobuf/proto"
)

func TestRegistryBindsTypedContributions(t *testing.T) {
	registry := New()
	called := make(map[string]bool)
	bindings := []aop.Binding{
		binding(&filepb.ProtocolMessage{}, func(context.Context, *aop.Envelope, proto.Message, aop.SendFunc) error {
			called["file"] = true
			return nil
		}),
		binding(&execpb.ProtocolMessage{}, func(context.Context, *aop.Envelope, proto.Message, aop.SendFunc) error {
			called["exec"] = true
			return nil
		}),
	}
	if _, err := registry.Add(bindings...); err != nil {
		t.Fatal(err)
	}

	mux := aop.NewNamespaceMux(t.Context())
	if err := registry.Bind(mux); err != nil {
		t.Fatal(err)
	}
	for name, message := range map[string]proto.Message{
		"file": &filepb.ProtocolMessage{},
		"exec": &execpb.ProtocolMessage{},
	} {
		handled, err := mux.Dispatch(aop.MustWrap(name, "", message), nil)
		if err != nil || !handled || !called[name] {
			t.Fatalf("dispatch %s: handled=%v called=%v err=%v", name, handled, called[name], err)
		}
	}
}

// A shared binding and a connection-scoped one contend for the same namespace:
// the opener is what differs between them, not the space they are registered in.
func TestRegistryRejectsDuplicateNamespace(t *testing.T) {
	registry := New()
	if _, err := registry.Add(binding(&filepb.ProtocolMessage{}, noOpHandler)); err != nil {
		t.Fatal(err)
	}
	if _, err := registry.Add(connection(&filepb.ProtocolMessage{})); err == nil {
		t.Fatal("duplicate protobuf namespace succeeded")
	}
	if _, err := New().Add(
		binding(&filepb.ProtocolMessage{}, noOpHandler),
		connection(&filepb.ProtocolMessage{}),
	); err == nil {
		t.Fatal("duplicate protobuf namespace in one contribution succeeded")
	}
}

func TestRegistryHandleRemovalAffectsFutureSnapshots(t *testing.T) {
	registry := New()
	handle, err := registry.Add(binding(&filepb.ProtocolMessage{}, noOpHandler))
	if err != nil {
		t.Fatal(err)
	}

	existing := aop.NewNamespaceMux(t.Context())
	if err := registry.Bind(existing); err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	envelope := aop.MustWrap("id", "", &filepb.ProtocolMessage{})
	handled, err := existing.Dispatch(envelope, nil)
	if err != nil || !handled {
		t.Fatalf("existing snapshot: handled=%v err=%v", handled, err)
	}

	future := aop.NewNamespaceMux(t.Context())
	if err := registry.Bind(future); err != nil {
		t.Fatal(err)
	}
	handled, err = future.Dispatch(envelope, nil)
	if err != nil || handled {
		t.Fatalf("future snapshot: handled=%v err=%v", handled, err)
	}
}

func TestRegistryOpensOneHandlerPerConnection(t *testing.T) {
	registry := New()
	fired := 0
	if _, err := registry.Add(aop.Binding{
		Prototype: &execpb.ProtocolMessage{},
		Open: func() aop.NamespaceHandler {
			fired++
			return noOpHandler
		},
	}); err != nil {
		t.Fatal(err)
	}

	envelope := aop.MustWrap("id", "", &execpb.ProtocolMessage{})
	for connection := range 2 {
		mux := aop.NewNamespaceMux(t.Context())
		if err := registry.Bind(mux); err != nil {
			t.Fatal(err)
		}
		handled, err := mux.Dispatch(envelope, nil)
		if err != nil || !handled {
			t.Fatalf("connection %d: handled=%v err=%v", connection, handled, err)
		}
		_ = mux.Close(t.Context())
	}
	if fired != 2 {
		t.Fatalf("opened %d handlers, want one per connection", fired)
	}
}

// Revoking before the first Bind leaves nothing to open, which is the case the
// future-snapshot test above cannot reach.
func TestRegistryRevokedBindingNeverReachesAConnection(t *testing.T) {
	registry := New()
	handle, err := registry.Add(connection(&filepb.ProtocolMessage{}))
	if err != nil {
		t.Fatal(err)
	}
	if err := handle.Close(t.Context()); err != nil {
		t.Fatal(err)
	}

	mux := aop.NewNamespaceMux(t.Context())
	if err := registry.Bind(mux); err != nil {
		t.Fatal(err)
	}
	if handled, err := mux.Dispatch(aop.MustWrap("id", "", &filepb.ProtocolMessage{}), nil); err != nil || handled {
		t.Fatalf("revoked binding: handled=%v err=%v", handled, err)
	}
}

func binding(prototype proto.Message, handler aop.NamespaceHandler) aop.Binding {
	return aop.Shared(prototype, handler)
}

func connection(prototype proto.Message) aop.Binding {
	return aop.Binding{Prototype: prototype, Open: func() aop.NamespaceHandler { return noOpHandler }}
}

func noOpHandler(context.Context, *aop.Envelope, proto.Message, aop.SendFunc) error { return nil }
