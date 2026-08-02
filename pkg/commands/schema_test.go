package commands

import (
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/tool"
)

type testReadArgs struct {
	Path   string `json:"path"            jsonschema:"description=File path to read"`
	Offset int    `json:"offset,omitempty" jsonschema:"description=Line offset"`
	Limit  int    `json:"limit,omitempty"  jsonschema:"description=Max lines"`
}

type testEnumArgs struct {
	Action string `json:"action" jsonschema:"description=Which action,enum=list,enum=peek,enum=kill"`
}

func TestSchemaOf(t *testing.T) {
	m := tool.SchemaOf(testReadArgs{})

	if m["type"] != "object" {
		t.Fatalf("expected type=object, got %v", m["type"])
	}

	props, ok := m["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties map, got %T", m["properties"])
	}
	if _, ok := props["path"]; !ok {
		t.Fatal("missing property: path")
	}
	if _, ok := props["offset"]; !ok {
		t.Fatal("missing property: offset")
	}

	required, _ := m["required"].([]any)
	found := false
	for _, r := range required {
		if r == "path" {
			found = true
		}
		if r == "offset" {
			t.Fatal("offset should not be required (has omitempty)")
		}
	}
	if !found {
		t.Fatal("path should be in required list")
	}
}

func TestSchemaOfEnum(t *testing.T) {
	m := tool.SchemaOf(testEnumArgs{})

	props, ok := m["properties"].(map[string]any)
	if !ok {
		t.Fatalf("expected properties map, got %T", m["properties"])
	}

	actionProp, ok := props["action"].(map[string]any)
	if !ok {
		t.Fatalf("expected action property map, got %T", props["action"])
	}

	enumVals, ok := actionProp["enum"].([]any)
	if !ok {
		t.Fatalf("expected enum array, got %T", actionProp["enum"])
	}

	expected := map[string]bool{"list": true, "peek": true, "kill": true}
	for _, v := range enumVals {
		s, _ := v.(string)
		if !expected[s] {
			t.Errorf("unexpected enum value: %v", v)
		}
		delete(expected, s)
	}
	if len(expected) > 0 {
		t.Errorf("missing enum values: %v", expected)
	}
}

func TestToolDef(t *testing.T) {
	def := tool.Def("read", "Read a file", testReadArgs{})

	if def.Type != "function" {
		t.Fatalf("expected type=function, got %s", def.Type)
	}
	if def.Name != "read" {
		t.Fatalf("expected name=read, got %s", def.Name)
	}
	if def.Description != "Read a file" {
		t.Fatalf("expected description='Read a file', got %s", def.Description)
	}
	if def.InputSchema == nil {
		t.Fatal("expected non-nil input schema")
	}
	params, err := aop.DecodeJSON[map[string]any](def.InputSchema)
	if err != nil {
		t.Fatalf("decode input schema: %v", err)
	}
	if params["type"] != "object" {
		t.Fatalf("expected parameters type=object, got %v", params["type"])
	}
}

func TestParseArgs(t *testing.T) {
	args, err := tool.ParseArgs[testReadArgs](`{"path": "/tmp/test.txt", "offset": 10}`)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if args.Path != "/tmp/test.txt" {
		t.Fatalf("expected path=/tmp/test.txt, got %s", args.Path)
	}
	if args.Offset != 10 {
		t.Fatalf("expected offset=10, got %d", args.Offset)
	}
	if args.Limit != 0 {
		t.Fatalf("expected limit=0 (default), got %d", args.Limit)
	}
}

func TestParseArgsInvalid(t *testing.T) {
	_, err := tool.ParseArgs[testReadArgs](`{invalid json}`)
	if err == nil {
		t.Fatal("expected error for invalid JSON")
	}
}

func TestToolResult(t *testing.T) {
	r := tool.TextResult("hello world")
	if tool.ResultText(r) != "hello world" {
		t.Fatalf("expected 'hello world', got %q", tool.ResultText(r))
	}
	if r.IsError {
		t.Fatal("expected IsError=false")
	}

	e := tool.ErrorResult("something broke")
	if !e.IsError {
		t.Fatal("expected IsError=true")
	}
	if tool.ResultText(e) != "something broke" {
		t.Fatalf("expected 'something broke', got %q", tool.ResultText(e))
	}

	tr := tool.TerminateResult("done")
	if !tr.Terminate {
		t.Fatal("expected Terminate=true")
	}
}
