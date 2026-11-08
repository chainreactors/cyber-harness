// Package collaboration defines Cyber Harness's application protocol. IOA only
// transports its opaque metadata; message and node references remain IOA's own.
package collaboration

import (
	"bytes"
	"encoding/json"
	"fmt"
)

const Version = 1

type Event string

const (
	Message          Event = "message"
	InboxRoute       Event = "inbox.route"
	SubagentDelegate Event = "subagent.delegate"
	SubagentReturn   Event = "subagent.return"
	TaskResult       Event = "task.result"
)

// Address identifies a logical task and/or a live session within an IOA node.
// Task IDs survive retries; session IDs identify individual executions. The
// containing IOA sender/refs.nodes supplies the default node scope. NodeID
// makes forwarded provenance explicit when the reporter differs from the source.
type Address struct {
	NodeID    string `json:"node_id,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

type Agent struct {
	Name  string `json:"name,omitempty"`
	Type  string `json:"type,omitempty"`
	Mode  string `json:"mode,omitempty"`
	Model string `json:"model,omitempty"`
}

// Envelope defines application fields directly in IOA meta. ID is an application event
// identity scoped by sender and source session, unchanged when IOA assigns a new transport ID on
// retry. Source/Target are affiliations and addresses, not inferred causal edges.
type Envelope struct {
	Version   int      `json:"version"`
	Event     Event    `json:"event"`
	ID        string   `json:"id,omitempty"`
	Source    Address  `json:"source,omitzero"`
	Target    Address  `json:"target,omitzero"`
	Interrupt bool     `json:"interrupt,omitempty"`
	Chain     []string `json:"chain,omitempty"`
	Agent     *Agent   `json:"agent,omitempty"`
	Status    string   `json:"status,omitempty"`
	CallID    string   `json:"call_id,omitempty"`
}

func (e Envelope) Validate() error {
	if e.Version != Version {
		return fmt.Errorf("unsupported collaboration version %d", e.Version)
	}
	switch e.Event {
	case Message:
		if e.Agent != nil || e.Status != "" || e.CallID != "" || len(e.Chain) != 0 {
			return fmt.Errorf("message cannot contain lifecycle or feedback fields")
		}
	case InboxRoute, SubagentDelegate, SubagentReturn, TaskResult:
		if e.ID == "" {
			return fmt.Errorf("%s requires a stable id", e.Event)
		}
	default:
		return fmt.Errorf("unknown collaboration event %q", e.Event)
	}
	if e.Event == InboxRoute || e.Event == SubagentDelegate {
		if e.Target.TaskID == "" && e.Target.SessionID == "" {
			return fmt.Errorf("%s requires a target", e.Event)
		}
	}
	if e.Event == SubagentReturn && e.Source.SessionID == "" {
		return fmt.Errorf("subagent.return requires a source session")
	}
	if e.Event != Message && e.Interrupt {
		return fmt.Errorf("%s cannot interrupt", e.Event)
	}
	if e.Event == InboxRoute && e.CallID != "" {
		return fmt.Errorf("inbox.route cannot contain a delegation call id")
	}
	if e.Event == TaskResult && (e.Agent != nil || e.CallID != "") {
		return fmt.Errorf("task.result cannot contain delegation fields")
	}
	if e.Event != TaskResult && len(e.Chain) != 0 {
		return fmt.Errorf("%s cannot contain a feedback chain", e.Event)
	}
	return nil
}

// IsField identifies the fields owned by the application protocol. Other IOA
// metadata remains opaque; CLI callers cannot override these reserved fields.
func IsField(key string) bool {
	switch key {
	case "version", "event", "id", "source", "target", "interrupt", "chain", "agent", "status", "call_id":
		return true
	default:
		return false
	}
}

// Meta uses the wire tags to produce direct metadata fields. Envelope has a
// fixed JSON-only schema, so these conversions cannot fail.
func (e Envelope) Meta() map[string]any {
	data, _ := json.Marshal(e)
	var meta map[string]any
	_ = json.Unmarshal(data, &meta)
	return meta
}

// Parse recognizes direct event metadata. Records without event are ordinary
// IOA data. Protocol fields are validated; unrelated dynamic keys stay opaque.
func Parse(meta map[string]any) (*Envelope, error) {
	if _, ok := meta["event"]; !ok {
		return nil, nil
	}
	fields := make(map[string]any)
	for key, value := range meta {
		if IsField(key) {
			if value == nil {
				return nil, fmt.Errorf("collaboration field %s cannot be null", key)
			}
			fields[key] = value
		}
	}
	data, err := json.Marshal(fields)
	if err != nil {
		return nil, fmt.Errorf("collaboration metadata: %w", err)
	}
	var e Envelope
	decoder := json.NewDecoder(bytes.NewReader(data))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&e); err != nil {
		return nil, fmt.Errorf("collaboration metadata: %w", err)
	}
	if err := e.Validate(); err != nil {
		return nil, err
	}
	return &e, nil
}
