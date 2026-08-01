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
	unsubscribe := subscribeAgentOutput(output, session, func(fn func(*aop.Event)) func() {
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
