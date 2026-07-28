package runner

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/webproto"
)

func TestProtocolErrorsKeepDistinctCorrelationIDs(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)

	var runError webproto.Message
	if !rt.HandleProtocol(context.Background(), webproto.Message{
		Type: webproto.TypeRun, TurnID: "turn-1", Payload: json.RawMessage(`{`),
	}, func(message webproto.Message) { runError = message }) {
		t.Fatal("run frame was not handled")
	}
	if runError.Type != webproto.TypeError || runError.TurnID != "turn-1" || runError.TaskID != "" {
		t.Fatalf("run error correlation = %+v", runError)
	}

	var commandError webproto.Message
	if !rt.HandleProtocol(context.Background(), webproto.Message{
		Type: webproto.TypeCommand, TaskID: "command-1", Payload: json.RawMessage(`{`),
	}, func(message webproto.Message) { commandError = message }) {
		t.Fatal("command frame was not handled")
	}
	if commandError.Type != webproto.TypeError || commandError.TaskID != "command-1" || commandError.TurnID != "" {
		t.Fatalf("command error correlation = %+v", commandError)
	}
}

func TestProtocolRequiresTurnID(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	var response webproto.Message
	rt.HandleProtocol(context.Background(), webproto.Message{
		Type: webproto.TypeRun, Payload: webproto.MustJSON(webproto.RunPayload{SessionID: "session-1"}),
	}, func(message webproto.Message) { response = message })
	var payload webproto.ErrorPayload
	_ = json.Unmarshal(response.Payload, &payload)
	if response.Type != webproto.TypeError || !strings.Contains(payload.Message, "turn_id is required") {
		t.Fatalf("response = %+v payload=%+v", response, payload)
	}
}

func TestProtocolSessionOpenIsIdempotent(t *testing.T) {
	rt := newBareRuntime(t, nil, nil)
	request := webproto.Message{
		Type:    webproto.TypeSessionOpen,
		Payload: webproto.MustJSON(webproto.SessionOpenPayload{SessionID: "session-1"}),
	}
	for i := 0; i < 2; i++ {
		var response webproto.Message
		if !rt.HandleProtocol(context.Background(), request, func(message webproto.Message) { response = message }) {
			t.Fatal("session.open was not handled")
		}
		if response.Type != webproto.TypeSessionOpened {
			t.Fatalf("open %d response = %+v", i, response)
		}
	}
}
