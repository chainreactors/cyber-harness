package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"

	coretool "github.com/chainreactors/cyber/core/tool"
)

func TestDiscoveredCLIMapsSchemaFlagsWithoutChangingValues(t *testing.T) {
	const inputSchema = `{"type":"object","required":["addr"],"properties":{
		"addr":{"type":"string","description":"Function address"},
		"count":{"type":"integer"},"ratio":{"type":"number"},"enabled":{"type":"boolean"},
		"nested":{"type":"object"},"items":{"type":"array"},"nothing":{"type":"null"},
		"nullable":{"anyOf":[{"type":"string"},{"type":"null"}]},
		"nullableType":{"type":["string","null"]},
		"choice":{"oneOf":[{"type":"string"},{"type":"integer"}]},
		"reference":{"$ref":"#/$defs/name"},"optional":{"type":"string","default":"upstream-owned"},
		"help":{"type":"string"},"json":{"type":"string"},"file":{"type":"string"},
		"unusual.name":{"type":"string"}
	},"$defs":{"name":{"type":"string"}},"additionalProperties":false}`
	var calls atomic.Int32
	var received atomic.Value
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			w.WriteHeader(http.StatusOK)
			return
		}
		var request struct {
			ID     json.RawMessage `json:"id"`
			Method string          `json:"method"`
			Params json.RawMessage `json:"params"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		var result string
		switch request.Method {
		case "initialize":
			result = `{"protocolVersion":"2025-03-26","capabilities":{"tools":{}},"serverInfo":{"name":"cli-test","version":"1"}}`
		case "notifications/initialized":
			w.WriteHeader(http.StatusAccepted)
			return
		case "tools/list":
			result = `{"tools":[{"name":"decompile","description":"Decompile a function","inputSchema":` + inputSchema + `}`
			for _, name := range []string{"tools", "schema", "call", "help", "--help", "--list", "-h", "--"} {
				result += fmt.Sprintf(`,{"name":%q,"inputSchema":{"type":"object"}}`, name)
			}
			result += `]}`
		case "tools/call":
			calls.Add(1)
			received.Store(string(request.Params))
			result = `{"content":[{"type":"text","text":"ok"}]}`
		default:
			t.Errorf("unexpected request %s", request.Method)
			return
		}
		_, _ = fmt.Fprintf(w, `{"jsonrpc":"2.0","id":%s,"result":%s}`, request.ID, result)
	}))
	defer upstream.Close()
	c := openHTTP(t, ServerConfig{URL: upstream.URL})
	catalog, err := c.Discover(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	command := catalog.Command()
	if command.Name != "ida" {
		t.Fatalf("server alias changed: %q", command.Name)
	}
	for _, test := range []struct {
		name string
		args []string
		want string
	}{
		{"omission", []string{"--addr", "0x401000"}, `{"addr":"0x401000"}`},
		{"empty string", []string{"--addr="}, `{"addr":""}`},
		{"leading dashes", []string{"--addr=--literal"}, `{"addr":"--literal"}`},
		{"typed values", []string{"--addr", "two words", "--count", "9007199254740993", "--ratio", "1.2300e+04", "--enabled", "false", "--nested", `{"inner":[null,true,9007199254740993]}`, "--items", `[1,"x",null]`, "--nothing", "null"}, `{"addr":"two words","count":9007199254740993,"ratio":1.2300e+04,"enabled":false,"nested":{"inner":[null,true,9007199254740993]},"items":[1,"x",null],"nothing":null}`},
		{"nullable strings and unions", []string{"--addr", "x", "--nullable", "null", "--nullableType", "hello", "--choice", "42", "--reference", `"ref value"`}, `{"addr":"x","nullable":"null","nullableType":"hello","choice":42,"reference":"ref value"}`},
		{"raw arbitrary fields and nulls", []string{"--json", `{"addr":"x","nullable":null,"help":"h","json":"j","file":"f","unusual.name":"u","extra":9007199254740993}`}, `{"addr":"x","nullable":null,"help":"h","json":"j","file":"f","unusual.name":"u","extra":9007199254740993}`},
	} {
		t.Run(test.name, func(t *testing.T) {
			var output bytes.Buffer
			_, err := command.Run(context.Background(), &coretool.Execution{Args: append([]string{"decompile"}, test.args...), Stdout: &output})
			if err != nil || !json.Valid(bytes.TrimSpace(output.Bytes())) {
				t.Fatalf("call: %s %v", &output, err)
			}
			var request struct {
				Name      string                     `json:"name"`
				Arguments map[string]json.RawMessage `json:"arguments"`
			}
			var want map[string]json.RawMessage
			if json.Unmarshal([]byte(received.Load().(string)), &request) != nil || json.Unmarshal([]byte(test.want), &want) != nil || request.Name != "decompile" || len(request.Arguments) != len(want) {
				t.Fatalf("arguments changed: %s", received.Load())
			}
			for name, value := range want {
				if !bytes.Equal(request.Arguments[name], value) {
					t.Fatalf("%s changed: got %s, want %s", name, request.Arguments[name], value)
				}
			}
		})
	}
	before := calls.Load()
	for _, args := range [][]string{
		{}, {"--unknown", "x"}, {"--addr", "x", "--addr", "y"}, {"--addr"}, {"--addr", "--enabled"},
		{"0x401000"}, {"--addr", "x", "--count", "1.5"}, {"--addr", "x", "--ratio", `"1"`},
		{"--addr", "x", "--enabled", "yes"}, {"--addr", "x", "--nested", "[]"}, {"--addr", "x", "--items", "{}"},
		{"--addr", "x", "--choice", "unquoted"}, {"--addr", "x", "--help", "value"},
		{"--addr", "x", "--file", "args.json"}, {"--json", "{}", "--addr", "x"},
	} {
		if _, err := command.Run(context.Background(), &coretool.Execution{Args: append([]string{"decompile"}, args...)}); err == nil {
			t.Fatalf("invalid flags accepted: %q", args)
		}
	}
	var help bytes.Buffer
	if _, err := command.Run(context.Background(), &coretool.Execution{Args: []string{"decompile", "--help"}, Stdout: &help}); err != nil {
		t.Fatal(err)
	}
	for _, text := range []string{"--addr <string> (required)", "Function address", "--nullable <string>", "--choice <JSON>", `"help" (use --json/--file)`, inputSchema} {
		if !strings.Contains(help.String(), text) {
			t.Fatalf("help lost %q: %s", text, &help)
		}
	}
	if calls.Load() != before {
		t.Fatal("invalid flags or help called a business tool")
	}
	// CLI control words never rename or hide upstream tool identities.
	for _, name := range []string{"tools", "schema", "call", "help", "--help", "--list", "-h", "--"} {
		args := []string{name}
		if strings.HasPrefix(name, "-") {
			args = []string{"--", name}
		}
		if _, err := command.Run(context.Background(), &coretool.Execution{Args: args}); err != nil {
			t.Fatalf("source tool %q was inaccessible: %v", name, err)
		}
		var request struct{ Name string }
		if json.Unmarshal([]byte(received.Load().(string)), &request) != nil || request.Name != name {
			t.Fatalf("source tool name changed: %s", received.Load())
		}
	}
}
