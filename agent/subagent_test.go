package agent

import (
	aop "github.com/chainreactors/cyber/aop"
	coreevents "github.com/chainreactors/cyber/core/events"
	types "github.com/chainreactors/cyber/core/types"
	"testing"
)

func TestSubAgentToolCallCarriesDelegationExtension(t *testing.T) {
	bus := coreevents.New()
	events := make(chan *aop.Event, 1)
	bus.Observe(coreevents.ObserverFunc(func(event *aop.Event) { events <- event }))
	em := newAOPEmitter(bus, "cyber", "parent-session", "", "", nil, 0)

	em.toolCall(&aop.ToolCall{
		Id:   "spawn-1",
		Name: "subagent",
		Kind: "function",
		Arguments: &aop.EncodedValue{
			Data:      []byte(`{"action":"create","prompt":"inspect the repository","name":"explorer","type":"reviewer","mode":"fork"}`),
			MediaType: aop.JSONMediaType,
		},
	})

	event := <-events
	detail, ok, err := types.GetDelegation(event)
	if err != nil || !ok {
		t.Fatalf("delegation ext = %#v, %v, %v", detail, ok, err)
	}
	if detail.Task != "inspect the repository" || detail.AgentName != "explorer" || detail.AgentType != "reviewer" {
		t.Fatalf("delegation detail = %#v", detail)
	}
	if detail.RunMode != types.DelegationRunBackground || detail.ContextMode != types.DelegationContextFork {
		t.Fatalf("delegation modes = %#v", detail)
	}
}
