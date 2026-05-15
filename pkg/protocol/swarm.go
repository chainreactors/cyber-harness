// Package protocol defines L2 application-layer message patterns on top of IOA.
//
// Swarm is a distributed agent coordination protocol. A coordinator
// (Human/Claude) and N heterogeneous agents share a single IOA Space
// (the "swarm"). Messages carry natural-language content with optional
// structured targets. IOA refs handle all routing and threading:
//
//   - Root message (no refs.messages) = task/instruction
//   - Reply (refs.messages=[task_id]) = report/response
//   - refs.nodes = routing (directed or broadcast)
package protocol

import "encoding/json"

// SwarmMessage is the single schema for all messages in a Swarm Space.
// Semantics come from IOA refs and sender, not from message fields:
//
//   - Coordinator sends root message with targets + content → task
//   - Agent sends reply referencing a task → report
//   - Meta carries sender self-description (IP, capabilities, network)
//
// All fields except Content are optional.
type SwarmMessage struct {
	Content string         `json:"content"`
	Targets []string       `json:"targets,omitempty"`
	Meta    map[string]any `json:"meta,omitempty"`
}

// SwarmSchema returns the JSON Schema for SwarmMessage, suitable for
// IOA content_schema validation on the Space.
func SwarmSchema() map[string]any {
	return map[string]any{
		"type": "object",
		"properties": map[string]any{
			"content": map[string]any{"type": "string"},
			"targets": map[string]any{
				"type":  "array",
				"items": map[string]any{"type": "string"},
			},
			"meta": map[string]any{"type": "object"},
		},
		"required":             []string{"content"},
		"additionalProperties": true,
	}
}

// ParseSwarm attempts to parse a raw IOA content map into a SwarmMessage.
func ParseSwarm(content map[string]any) (SwarmMessage, bool) {
	c, ok := content["content"].(string)
	if !ok || c == "" {
		return SwarmMessage{}, false
	}
	msg := SwarmMessage{Content: c}
	if raw, ok := content["targets"]; ok {
		if data, err := json.Marshal(raw); err == nil {
			_ = json.Unmarshal(data, &msg.Targets)
		}
	}
	if raw, ok := content["meta"].(map[string]any); ok {
		msg.Meta = raw
	}
	return msg, true
}

// ParseLegacyTask converts the old {"type":"task","task":"..."} format
// into a SwarmMessage for backward compatibility.
func ParseLegacyTask(content map[string]any) (SwarmMessage, bool) {
	if task, ok := content["task"].(string); ok && task != "" {
		return SwarmMessage{Content: task}, true
	}
	if prompt, ok := content["prompt"].(string); ok && prompt != "" {
		return SwarmMessage{Content: prompt}, true
	}
	return SwarmMessage{}, false
}
