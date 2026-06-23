package output

import (
	"encoding/json"
	"testing"
)

func TestLootRecordRoundTrip(t *testing.T) {
	loot := Loot{
		Kind:        LootVuln,
		Target:      "http://10.0.0.1:8080",
		Priority:    "high",
		Description: "CVE-2024-1234 — Remote Code Execution",
		Tags:        []string{"high", "CVE-2024-1234"},
		Data: map[string]any{
			"key":           "http://10.0.0.1:8080|CVE-2024-1234",
			"template_id":   "CVE-2024-1234",
			"template_name": "Remote Code Execution",
			"severity":      "high",
		},
	}
	rec := NewLootRecord(TypeNeutron, loot)

	line := rec.Marshal()
	parsed, err := ParseRecord(line)
	if err != nil {
		t.Fatalf("ParseRecord: %v", err)
	}
	if parsed.Type != TypeNeutron {
		t.Fatalf("type = %s, want neutron", parsed.Type)
	}
	if !parsed.Loot {
		t.Fatal("loot flag not set")
	}

	got, err := ParseRecordData[Loot](parsed)
	if err != nil {
		t.Fatalf("ParseRecordData: %v", err)
	}
	if got.Kind != LootVuln {
		t.Fatalf("kind = %s, want vuln", got.Kind)
	}
	if got.Target != "http://10.0.0.1:8080" {
		t.Fatalf("target = %s", got.Target)
	}
	if got.Priority != "high" {
		t.Fatalf("priority = %s", got.Priority)
	}
	if got.Description != "CVE-2024-1234 — Remote Code Execution" {
		t.Fatalf("description = %s", got.Description)
	}
	if got.Key() != "vuln|http://10.0.0.1:8080|http://10.0.0.1:8080|CVE-2024-1234" {
		t.Fatalf("key = %s", got.Key())
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
