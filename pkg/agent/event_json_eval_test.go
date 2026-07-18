package agent

import (
	"encoding/json"
	"testing"
)

// TestEventMarshalIncludesEvalVerdict pins the fix for the deepest layer of the
// Goal-mode "eval badge missing" bug: Event.MarshalJSON is an allowlist, and it
// used to omit the evaluator verdict fields entirely, so the per-round pass/
// reason never left the agent process over the WS wire. Guard that they now
// serialize (as snake_case, matching the hub decoder).
func TestEventMarshalIncludesEvalVerdict(t *testing.T) {
	e := Event{Type: EventEvalEnd, EvalRound: 2, EvalPass: true, EvalReason: "found SQLi"}
	b, err := json.Marshal(e)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}
	var got struct {
		Type       string `json:"type"`
		EvalRound  int    `json:"eval_round"`
		EvalPass   bool   `json:"eval_pass"`
		EvalReason string `json:"eval_reason"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v (raw=%s)", err, b)
	}
	if got.Type != "eval_end" || got.EvalRound != 2 || !got.EvalPass || got.EvalReason != "found SQLi" {
		t.Fatalf("verdict not serialized: raw=%s", b)
	}
}

// A judge error carries its message in eval_error; the hub renders it as the
// verdict reason.
func TestEventMarshalIncludesEvalError(t *testing.T) {
	e := Event{Type: EventEvalError, EvalRound: 0, EvalError: "judge timed out"}
	b, _ := json.Marshal(e)
	var got struct {
		EvalError string `json:"eval_error"`
	}
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatalf("unmarshal: %v (raw=%s)", err, b)
	}
	if got.EvalError != "judge timed out" {
		t.Fatalf("eval_error not serialized: raw=%s", b)
	}
}
