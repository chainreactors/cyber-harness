package collaboration

import (
	"encoding/json"
	"reflect"
	"testing"
)

func TestApplicationProtocolRoundTrip(t *testing.T) {
	for _, record := range []Envelope{
		{Version: Version, Event: Message, ID: "input", Source: Address{SessionID: "peer"}, Target: Address{SessionID: "worker"}, Interrupt: true},
		{Version: Version, Event: InboxRoute, ID: "route", Source: Address{TaskID: "upstream"}, Target: Address{TaskID: "durable"}, Agent: &Agent{Name: "audit", Mode: "active"}, Status: "routed"},
		{Version: Version, Event: SubagentDelegate, ID: "delegate-child", Source: Address{SessionID: "parent"}, Target: Address{TaskID: "durable", SessionID: "child"}, CallID: "call", Agent: &Agent{Type: "audit", Model: "model", Mode: "fork"}},
		{Version: Version, Event: SubagentReturn, ID: "return-child", Source: Address{TaskID: "durable", SessionID: "child"}, Target: Address{SessionID: "parent"}, Status: "completed"},
		{Version: Version, Event: TaskResult, ID: "result", Source: Address{TaskID: "durable", SessionID: "child"}, Chain: []string{"upstream", "durable"}, Status: "completed"},
	} {
		t.Run(string(record.Event), func(t *testing.T) {
			meta := record.Meta()
			meta["foreign.app"] = map[string]any{"arbitrary": []any{true, 3, "x"}}
			data, err := json.Marshal(meta)
			if err != nil {
				t.Fatal(err)
			}
			var wire map[string]any
			if err := json.Unmarshal(data, &wire); err != nil {
				t.Fatal(err)
			}
			parsed, err := Parse(wire)
			if err != nil || parsed == nil || !reflect.DeepEqual(record, *parsed) {
				t.Fatalf("round trip: %#v %v", parsed, err)
			}
			if wire["event"] != string(record.Event) || wire["cyber.harness"] != nil {
				t.Fatalf("application fields must be direct metadata: %s", data)
			}
		})
	}
}

func TestApplicationProtocolRejectsMalformedAndForeignVersions(t *testing.T) {
	for name, value := range map[string]any{
		"null-event":             map[string]any{"version": 1, "event": nil},
		"wrong-event-type":       map[string]any{"version": 1, "event": []any{}},
		"missing-version":        map[string]any{"event": "message"},
		"future-version":         map[string]any{"version": 2, "event": "message"},
		"unknown-event":          map[string]any{"version": 1, "event": "delegate"},
		"null-address":           map[string]any{"version": 1, "event": "message", "source": nil},
		"unknown-address":        map[string]any{"version": 1, "event": "message", "target": map[string]any{"worker_id": "unknown"}},
		"wrong-address-type":     map[string]any{"version": 1, "event": "message", "target": map[string]any{"session_id": 10}},
		"wrong-interrupt-type":   map[string]any{"version": 1, "event": "message", "interrupt": "true"},
		"message-feedback-chain": Envelope{Version: Version, Event: Message, Chain: []string{"task"}},
		"route-no-id":            Envelope{Version: Version, Event: InboxRoute, Target: Address{TaskID: "task"}},
		"delegate-no-target":     Envelope{Version: Version, Event: SubagentDelegate, ID: "stable"},
		"route-tool-call":        Envelope{Version: Version, Event: InboxRoute, ID: "stable", Target: Address{TaskID: "task"}, CallID: "call"},
		"node-only-delegate":     Envelope{Version: Version, Event: SubagentDelegate, ID: "stable", Target: Address{NodeID: "node"}},
		"return-no-execution":    Envelope{Version: Version, Event: SubagentReturn, ID: "stable", Source: Address{TaskID: "task"}},
		"result-interrupt":       Envelope{Version: Version, Event: TaskResult, ID: "stable", Interrupt: true},
		"result-delegation":      Envelope{Version: Version, Event: TaskResult, ID: "stable", CallID: "call"},
		"message-lifecycle":      Envelope{Version: Version, Event: Message, Agent: &Agent{}},
	} {
		t.Run(name, func(t *testing.T) {
			meta, _ := value.(map[string]any)
			if record, ok := value.(Envelope); ok {
				meta = record.Meta()
			}
			if e, err := Parse(meta); err == nil || e != nil {
				t.Fatalf("accepted invalid application data: %#v", value)
			}
		})
	}
}

func TestOnlyDirectApplicationEventsAreInterpreted(t *testing.T) {
	for _, meta := range []map[string]any{nil, {"other.app": map[string]any{"version": 999}}, {"cyber.harness": Envelope{Version: Version, Event: Message}}, {"target_session_id": "old", "interrupt": true, "subagent": map[string]any{"phase": "delegate"}}} {
		if e, err := Parse(meta); err != nil || e != nil {
			t.Fatalf("generic IOA metadata acquired Harness semantics: %#v %v", e, err)
		}
	}
}

func TestOptionalAddressesDoNotProduceEmptyWireObjects(t *testing.T) {
	data, err := json.Marshal(Envelope{Version: Version, Event: Message}.Meta())
	if err != nil {
		t.Fatal(err)
	}
	var wire map[string]json.RawMessage
	if err = json.Unmarshal(data, &wire); err != nil {
		t.Fatal(err)
	}
	if len(wire) != 2 {
		t.Fatalf("zero addresses leaked into wire envelope: %s", data)
	}
}
