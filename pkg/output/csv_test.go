package output

import (
	"bytes"
	"encoding/csv"
	"os"
	"path/filepath"
	"strings"
	"testing"

	aop "github.com/chainreactors/cyber/aop"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestCSVReplayExportsNativeArtifactsAndFinalVerification(t *testing.T) {
	dir := t.TempDir()
	input, output := filepath.Join(dir, "scan.jsonl"), filepath.Join(dir, "scan.csv")
	var stream bytes.Buffer
	write := func(id, session string, message proto.Message) {
		t.Helper()
		extension, err := anypb.New(message)
		if err != nil {
			t.Fatal(err)
		}
		event := &aop.Event{Id: id, SessionId: session, TurnId: "turn", Payload: &aop.Event_Extension{Extension: extension}}
		line, err := protojson.Marshal(event)
		if err != nil {
			t.Fatal(err)
		}
		stream.Write(line)
		stream.WriteByte('\n')
	}
	service := &toolpb.Artifact{ResultId: "same-id", Tool: "gogo", Kind: "service", Target: "127.0.0.1:80",
		Data: []byte(`{"ip":"127.0.0.1","port":"80","title":"中文, \"标题\"\n下一行","serial":9007199254740993,"frameworks":{"nginx":{}},"optional":null}`)}
	write("service", "first", service)
	write("service", "first", service) // Duplicate event delivery.
	write("loot", "first", &toolpb.Loot{ResultId: "same-id", Tool: "neutron", Priority: "critical", VerificationStatus: "confirmed", Description: "validated", Tags: []string{"rce", "test"}})
	write("vuln", "first", &toolpb.Artifact{ResultId: "same-id", Tool: "neutron", Kind: "vuln", Target: "http://127.0.0.1", Data: []byte(`{"template_id":"test-rce","matched":true}`)})
	write("other-session", "second", service)
	write("chat", "first", &aop.Status{State: "irrelevant"})
	if err := os.WriteFile(input, stream.Bytes(), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RenderEventFile(input, "CSV", output); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(output)
	if err != nil {
		t.Fatal(err)
	}
	records, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil || len(records) != 4 {
		t.Fatalf("records=%v err=%v", records, err)
	}
	value := func(row int, name string) string {
		t.Helper()
		for i, field := range records[0] {
			if field == name {
				return records[row][i]
			}
		}
		t.Fatalf("missing column %s", name)
		return ""
	}
	for name, want := range map[string]string{"target": "127.0.0.1:80", "data.ip": "127.0.0.1", "data.port": "80", "data.title": "中文, \"标题\"\n下一行", "data.serial": "9007199254740993", "data.frameworks": `{"nginx":{}}`, "data.optional": "null", "verification_status": ""} {
		// encoding/csv normalizes CRLF inside quoted fields to LF.
		if got := value(1, name); got != want {
			t.Errorf("service %s=%q, want %q", name, got, want)
		}
	}
	for name, want := range map[string]string{"tool": "neutron", "priority": "critical", "verification_status": "confirmed", "description": "validated", "tags": "rce; test", "data.matched": "true"} {
		if got := value(2, name); got != want {
			t.Errorf("vulnerability %s=%q, want %q", name, got, want)
		}
	}
	if value(3, "session_id") != "second" || value(3, "verification_status") != "" {
		t.Fatal("verification crossed session/tool identity")
	}
	if retained, _ := os.ReadFile(input); !bytes.Equal(retained, stream.Bytes()) {
		t.Fatal("replay changed source events")
	}
}

func TestReplayRejectsInvalidFormatAndPreservesSourceAndDestination(t *testing.T) {
	dir := t.TempDir()
	input, output := filepath.Join(dir, "scan.jsonl"), filepath.Join(dir, "scan.csv")
	source := []byte("{\"id\":\"one\",\"sessionId\":\"session\",\"status\":{\"state\":\"ready\"}}\n")
	if err := os.WriteFile(input, source, 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(output, []byte("preserve"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := RenderEventFile(input, "cvs", output); err == nil || !strings.Contains(err.Error(), "unsupported output format") {
		t.Fatalf("format error=%v", err)
	}
	if data, _ := os.ReadFile(output); string(data) != "preserve" {
		t.Fatal("invalid format truncated destination")
	}
	if err := RenderEventFile(input, "csv", input); err == nil {
		t.Fatal("rendering accepted its own source as destination")
	}
	if data, _ := os.ReadFile(input); !bytes.Equal(data, source) {
		t.Fatal("source was changed")
	}
	if err := RenderEventFile(input, "csv", output); err != nil {
		t.Fatal(err)
	}
	data, _ := os.ReadFile(output)
	records, err := csv.NewReader(bytes.NewReader(data)).ReadAll()
	if err != nil || len(records) != 1 {
		t.Fatalf("empty scan must export headers only: records=%v err=%v", records, err)
	}
}
