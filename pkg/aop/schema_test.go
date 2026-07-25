package aop

import (
	"bufio"
	"bytes"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	jsonschema "github.com/santhosh-tekuri/jsonschema/v6"
)

func TestCanonicalCyberUIFixtures(t *testing.T) {
	protocolRoot := filepath.Join("..", "..", "web", "frontend", "cyber-ui", "packages", "agent-protocol")
	fixtureRoot := filepath.Join(protocolRoot, "fixtures")
	compiler := jsonschema.NewCompiler()
	err := filepath.WalkDir(filepath.Join(protocolRoot, "schema"), func(path string, entry os.DirEntry, walkErr error) error {
		if walkErr != nil || entry.IsDir() || filepath.Ext(path) != ".json" {
			return walkErr
		}
		data, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		var document map[string]any
		if err := json.Unmarshal(data, &document); err != nil {
			return err
		}
		id, _ := document["$id"].(string)
		if id != "" {
			return compiler.AddResource(id, document)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	schema, err := compiler.Compile("https://github.com/chainreactors/cyber-ui/packages/agent-protocol/schema/aop.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	delegationSchema, err := compiler.Compile("https://github.com/chainreactors/cyber-ui/packages/agent-protocol/schema/ext/delegation.schema.json")
	if err != nil {
		t.Fatal(err)
	}
	paths, err := filepath.Glob(filepath.Join(fixtureRoot, "*.jsonl"))
	if err != nil {
		t.Fatal(err)
	}

	var (
		seenReasoningDelta = false
		seenComplete       = false
		seenStatusExt      = false
	)
	for _, path := range paths {
		content, err := os.ReadFile(path)
		if err != nil {
			t.Fatal(err)
		}
		scanner := bufio.NewScanner(bytes.NewReader(content))
		for scanner.Scan() {
			var document any
			if err := json.Unmarshal(scanner.Bytes(), &document); err != nil {
				t.Fatalf("decode fixture document %s: %v", path, err)
			}
			if err := schema.Validate(document); err != nil {
				t.Fatalf("schema validation failed for %s: %v\n%s", path, err, scanner.Text())
			}
			var event Event
			if err := json.Unmarshal(scanner.Bytes(), &event); err != nil {
				t.Fatalf("decode fixture %s: %v", path, err)
			}
			if !event.Valid() {
				t.Fatalf("invalid fixture envelope in %s: %+v", path, event)
			}
			if raw, ok := event.Ext["delegation"]; ok {
				var detail any
				if err := json.Unmarshal(raw, &detail); err != nil {
					t.Fatalf("decode delegation fixture %s: %v", path, err)
				}
				if err := delegationSchema.Validate(detail); err != nil {
					t.Fatalf("delegation schema validation failed for %s: %v", path, err)
				}
			}
			switch event.Type {
			case TypeMessage:
				var data MessageData
				if err := json.Unmarshal(event.Data, &data); err != nil {
					t.Fatal(err)
				}
				if data.MessageID == "" || data.Role == "" || len(data.Parts) == 0 {
					t.Fatalf("invalid message payload: %+v", data)
				}
				seenComplete = true
			case TypeMessageDelta:
				var data MessageDeltaData
				if err := json.Unmarshal(event.Data, &data); err != nil {
					t.Fatal(err)
				}
				if data.MessageID == "" || data.PartType == "" {
					t.Fatalf("invalid message.delta payload: %+v", data)
				}
				seenReasoningDelta = seenReasoningDelta || data.PartType == PartReasoning
			case TypeStatus:
				if len(event.Ext) > 0 {
					seenStatusExt = true
				}
			}
		}
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
	}
	if !seenReasoningDelta {
		t.Fatal("canonical fixtures do not cover reasoning deltas")
	}
	if !seenComplete {
		t.Fatal("canonical fixtures do not cover complete messages")
	}
	if !seenStatusExt {
		t.Fatal("canonical fixtures do not cover status ext payloads")
	}
}
