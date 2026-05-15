package swarm

import (
	"encoding/json"

	"github.com/chainreactors/ioa"
)

type SwarmMessage struct {
	Content string         `json:"content"`
	Targets []string       `json:"targets,omitempty"`
	Meta    map[string]any `json:"meta,omitempty"`
}

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

func ParseLegacyTask(content map[string]any) (SwarmMessage, bool) {
	if task, ok := content["task"].(string); ok && task != "" {
		return SwarmMessage{Content: task}, true
	}
	if prompt, ok := content["prompt"].(string); ok && prompt != "" {
		return SwarmMessage{Content: prompt}, true
	}
	return SwarmMessage{}, false
}

func swarmContent(msg SwarmMessage) map[string]any {
	m := map[string]any{"content": msg.Content}
	if len(msg.Targets) > 0 {
		m["targets"] = msg.Targets
	}
	if len(msg.Meta) > 0 {
		m["meta"] = msg.Meta
	}
	return m
}

func swarmFromIOA(msg ioa.Message) (SwarmMessage, bool) {
	if sm, ok := ParseSwarm(msg.Content); ok {
		return sm, true
	}
	return ParseLegacyTask(msg.Content)
}

func isProfileMessage(msg SwarmMessage) bool {
	kind, _ := msg.Meta["kind"].(string)
	return kind == "node_profile"
}
