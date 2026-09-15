package api

import (
	"context"
	aop "github.com/chainreactors/cyber/aop"
	filepb "github.com/chainreactors/cyber/aop/file"
	"google.golang.org/protobuf/proto"
	"testing"
)

type fixtureApplicationConnection struct {
	t     *testing.T
	sends []*aop.Envelope
	ran   bool
}

func (c *fixtureApplicationConnection) Context() context.Context { return c.t.Context() }
func (c *fixtureApplicationConnection) Send(e *aop.Envelope) error {
	c.sends = append(c.sends, e)
	return nil
}
func (c *fixtureApplicationConnection) Run(e *aop.Envelope, dispatch func(context.Context, *aop.Envelope, aop.SendFunc) error) error {
	c.ran = true
	return dispatch(c.Context(), e, c.Send)
}
func TestSessionOnlyApplicationCanAddItsOwnNamespace(t *testing.T) {
	first, err := aop.Wrap("request", "", &filepb.ProtocolMessage{})
	if err != nil {
		t.Fatal(err)
	}
	called := false
	connection := &fixtureApplicationConnection{t: t}
	backends := &ApplicationBackends{Sessions: &Sessions{}, NewID: func() string { return "reply" }, RegisterNamespaces: func(mux *aop.NamespaceMux) error {
		return mux.Register("fixture", &filepb.ProtocolMessage{}, func(context.Context, *aop.Envelope, proto.Message, aop.SendFunc) error { called = true; return nil })
	}}
	if err := ServeApplication(connection, first, backends); err != nil {
		t.Fatal(err)
	}
	if !called || !connection.ran {
		t.Fatal("session-only application did not dispatch its extension")
	}
	backends.RegisterNamespaces = func(mux *aop.NamespaceMux) error {
		return mux.Register("fixture", &aop.ProtocolMessage{}, func(context.Context, *aop.Envelope, proto.Message, aop.SendFunc) error { return nil })
	}
	rejected := &fixtureApplicationConnection{t: t}
	if err := ServeApplication(rejected, first, backends); err == nil {
		t.Fatal("accepted duplicate namespace")
	}
	if rejected.ran {
		t.Fatal("published invalid connection bindings")
	}
}
