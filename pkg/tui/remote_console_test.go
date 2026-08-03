package tui

import (
	"bytes"
	"testing"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
)

func TestSubscribeAgentOutputRestoresSessionEvents(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	output := NewAgentOutputWithWriters(nil, &stdout, &stderr, true)
	defer output.live.Stop()

	session := agent.NewAgent(agent.Config{SessionID: "main-repl"})
	var handler func(*aop.Event)
	unsubscribed := false
	unsubscribe := subscribeAgentOutput(output, AppInfo{}, session, func(fn func(*aop.Event)) func() {
		handler = fn
		return func() { unsubscribed = true }
	})

	other := turnStartEvent(1)
	other.SessionId = "other-session"
	handler(other)
	if liveRunning(output.live) {
		t.Fatal("output consumed an event from another runtime session")
	}

	current := turnStartEvent(1)
	current.SessionId = "main-repl"
	handler(current)
	if !liveRunning(output.live) {
		t.Fatal("session turn.start did not restore the thinking status")
	}

	unsubscribe()
	if !unsubscribed {
		t.Fatal("event subscription was not released")
	}
}

func TestSubscribeAgentOutputTracksRotatedRuntimeSession(t *testing.T) {
	var stdout bytes.Buffer
	var stderr syncedBuffer
	output := NewAgentOutputWithWriters(nil, &stdout, &stderr, true)
	defer output.live.Stop()

	activeID := "session-old"
	session := agent.NewAgent(agent.Config{SessionID: activeID})
	var handler func(*aop.Event)
	unsubscribe := subscribeAgentOutput(output, AppInfo{ActiveSessionID: func() string { return activeID }}, session, func(fn func(*aop.Event)) func() {
		handler = fn
		return func() {}
	})
	defer unsubscribe()

	activeID = "session-new"
	old := turnStartEvent(1)
	old.SessionId = "session-old"
	handler(old)
	if liveRunning(output.live) {
		t.Fatal("output consumed an event from the rotated-out session")
	}
	current := turnStartEvent(1)
	current.SessionId = "session-new"
	handler(current)
	if !liveRunning(output.live) {
		t.Fatal("output did not follow the rotated runtime session")
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
