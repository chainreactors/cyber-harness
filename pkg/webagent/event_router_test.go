package webagent

import (
	"encoding/json"
	"sync"
	"testing"

	"github.com/chainreactors/aiscan/pkg/aop"
)

func newTestRouter() *eventRouter {
	return &eventRouter{
		mu:         &sync.Mutex{},
		eventRoute: map[string]string{},
	}
}

func bracketEvent(typ, sessionID string) aop.Event {
	data, _ := json.Marshal(aop.SessionEndData{Stop: "completed"})
	return aop.Event{Type: typ, SessionID: sessionID, Data: data}
}

func TestSuppressSessionBrackets(t *testing.T) {
	router := newTestRouter()

	router.SuppressSessionBrackets("sess-eval")

	if !router.suppressBrackets(bracketEvent(aop.TypeSessionStart, "sess-eval")) {
		t.Fatal("session.start should be suppressed")
	}
	if !router.suppressBrackets(bracketEvent(aop.TypeSessionEnd, "sess-eval")) {
		t.Fatal("session.end should be suppressed")
	}

	// Non-bracket events and other sessions pass through.
	if router.suppressBrackets(bracketEvent(aop.TypeMessage, "sess-eval")) {
		t.Fatal("message events must not be suppressed")
	}
	if router.suppressBrackets(bracketEvent(aop.TypeSessionEnd, "sess-other")) {
		t.Fatal("other sessions must not be suppressed")
	}

	router.UnsuppressSessionBrackets("sess-eval")
	if router.suppressBrackets(bracketEvent(aop.TypeSessionEnd, "sess-eval")) {
		t.Fatal("unsuppress should restore forwarding")
	}
}
