package mcp

import (
	"encoding/json"
	"testing"
)

func TestParseFlatConfig(t *testing.T) {
	flat := `{"ida":{"url":"https://example.test/mcp","headers":{"Authorization":"Bearer private"},"tools":[]}}`
	servers, err := Parse([]byte(flat))
	if err != nil {
		t.Fatal(err)
	}
	if servers["ida"].Tools == nil {
		t.Fatal("explicit empty allowlist was lost")
	}
	data, err := json.Marshal(servers)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, nested := fields["mcpServers"]; nested || fields["ida"] == nil {
		t.Fatalf("unnecessary wrapper returned: %s", data)
	}
	for _, input := range []string{
		`{"mcpServers":{"ida":{"url":"https://example.test"}}}`, `null`, `[]`, `{"mcpServers":null}`, `{"mcpServers":[],"ida":{}}`,
		`{"mcpServers":{},"ida":{"url":"https://example.test"}}`,
		`{"ida":{"url":"https://example.test","typo":true}}`,
		`{"mcpServers":{"ida":{"url":"https://example.test","typo":true}}}`,
		`{"ida":null}`, `{"ida":{"command":"python","url":"https://example.test"}}`,
		`{"ida":{"url":"https://example.test"}} {}`,
	} {
		if _, err := Parse([]byte(input)); err == nil {
			t.Fatalf("invalid MCP configuration accepted: %s", input)
		}
	}
}
