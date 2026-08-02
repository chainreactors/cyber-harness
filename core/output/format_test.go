package output

import (
	"encoding/json"
	"testing"
)

func TestStripANSIPrivateModeSequences(t *testing.T) {
	input := "\x1b[?9001h\x1b[?1004h\x1b[?25lchat_pass \x1b[?25h"
	if got := StripANSI(input); got != "chat_pass " {
		t.Fatalf("StripANSI = %q, want %q", got, "chat_pass ")
	}
}

func TestLootJSONSchema(t *testing.T) {
	loot := Loot{
		Kind:        LootWeakpass,
		Target:      "10.0.0.1:22",
		Priority:    "high",
		Description: "ssh root/toor",
		Tags:        []string{"ssh"},
		Data: map[string]any{
			"key":      "ssh|10.0.0.1:22|root|toor",
			"service":  "ssh",
			"username": "root",
			"password": "toor",
		},
	}

	data, err := json.Marshal(loot)
	if err != nil {
		t.Fatalf("Marshal: %v", err)
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		t.Fatalf("Unmarshal: %v", err)
	}

	for _, field := range []string{"kind", "target", "priority", "description", "tags", "data"} {
		if _, ok := m[field]; !ok {
			t.Fatalf("missing JSON field %q", field)
		}
	}
	if m["kind"] != "weakpass" {
		t.Fatalf("kind = %v", m["kind"])
	}
}
