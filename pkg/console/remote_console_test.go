package console

import (
	"bytes"
	"context"
	"testing"

	aop "github.com/chainreactors/cyber/aop"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func TestSubscribeAgentOutputTracksRotatedRuntimeSession(t *testing.T) {
	var stdout, stderr bytes.Buffer
	c, application := newTestConsole(t, &cfg.Option{}, nil, &stdout, &stderr)
	oldID := c.session.ID()
	if _, err := c.session.Command(context.Background(), "/clear"); err != nil {
		t.Fatal(err)
	}
	stdout.Reset()
	emit := func(id, text string) {
		application.Stream.Publish(&aop.Event{SessionId: id, Payload: &aop.Event_Message{Message: &aop.Message{Id: "command", Role: "assistant", Content: []*aop.Content{aop.Text(text)}}}})
	}
	emit(oldID, "stale")
	emit("sibling", "sibling")
	emit(c.session.ID(), "current")
	if stdout.String() != "current\n" {
		t.Fatalf("output=%q", stdout.String())
	}
	c.Close()
	emit(c.session.ID(), "late")
	if stdout.String() != "current\n" {
		t.Fatal("subscription survived close")
	}
}
func TestSessionBootstrapEventsAreNotRenderedAsLiveOutput(t *testing.T) {
	bootstrap := &aop.Event{SessionId: "next", Payload: &aop.Event_Message{Message: &aop.Message{
		Id: "m-4", Role: "assistant", Content: []*aop.Content{aop.Text("restored history")},
	}}}
	if !isSessionBootstrapEvent(bootstrap) {
		t.Fatal("restored message was not recognized as a bootstrap event")
	}
	bootstrap.TurnId = "turn-1"
	if isSessionBootstrapEvent(bootstrap) {
		t.Fatal("live turn message was mistaken for bootstrap history")
	}
}
