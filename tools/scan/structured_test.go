package scan

import (
	"encoding/json"
	"testing"

	"github.com/chainreactors/utils/parsers"
)

func TestLootJSONSchema(t *testing.T) {
	loot := parsers.Loot{
		Kind:        parsers.LootWeakpass,
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
