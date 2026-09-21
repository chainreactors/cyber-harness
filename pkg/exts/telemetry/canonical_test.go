package telemetry_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"

	"github.com/chainreactors/cyber/core/eventbus"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"

	"os"
	"path/filepath"
	"testing"

	coreevents "github.com/chainreactors/cyber/core/events"
	telemetryext "github.com/chainreactors/cyber/pkg/exts/telemetry"

	aop "github.com/chainreactors/cyber/aop"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/utils/parsers"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestJSONLRecorderPersistsCanonicalEventsAndOneArtifactPerResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	events := coreevents.New()
	recorder, err := telemetryext.New(telemetryext.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	app := events
	appSet := hosttest.Set(t,
		extension.Provided[*coreevents.Stream](events),
		recorder,
	)
	if err := appSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	app.Publish(&aop.Event{
		SessionId: "session-1", TurnId: "turn-1", Emitter: "cyber",
		Payload: &aop.Event_ToolCall{ToolCall: &aop.ToolCall{Id: "call-1", Name: "gogo"}},
	})
	eventbus.New[*toolpb.Progress]().Emit(&toolpb.Progress{Tool: "gogo", Text: "raw PTY bytes", CallId: "call-1"})
	gogoResult := parsers.NewGOGOResult("127.0.0.1", "443")
	gogoResult.Protocol = "https"
	raw, err := json.Marshal(gogoResult)
	if err != nil {
		t.Fatal(err)
	}
	artifactEvent := &aop.Event{
		SessionId: "session-1", TurnId: "turn-1", Emitter: "cyber",
	}
	artifactExtension, err := anypb.New(&toolpb.Artifact{
		Tool: "gogo", Kind: toolpb.ArtifactKindService, Target: gogoResult.GetTarget(), Data: raw,
		MediaType: aop.JSONMediaType,
	})
	if err != nil {
		t.Fatal(err)
	}
	artifactEvent.Payload = &aop.Event_Extension{Extension: artifactExtension}
	if err := aop.SetTypedExtension(artifactEvent, &operationpb.Ref{CallId: "call-1"}); err != nil {
		t.Fatal(err)
	}
	app.Publish(artifactEvent)
	app.Publish(&aop.Event{
		SessionId: "session-1", TurnId: "turn-1", Emitter: "cyber",
		Payload: &aop.Event_ToolResult{ToolResult: &aop.ToolResult{CallId: "call-1", Name: "gogo"}},
	})
	if err := appSet.Close(context.Background()); err != nil {
		t.Fatal(err)
	}

	file, err := os.Open(path)
	if err != nil {
		t.Fatalf("open JSONL: %v", err)
	}
	defer file.Close()
	counts := map[string]int{}
	var artifact toolpb.Artifact
	var artifactRef operationpb.Ref
	scanner := bufio.NewScanner(file)
	for scanner.Scan() {
		line := scanner.Bytes()
		if len(bytes.TrimSpace(line)) == 0 {
			t.Fatal("JSONL contains a blank line")
		}
		event := new(aop.Event)
		if err := protojson.Unmarshal(line, event); err != nil {
			t.Fatalf("JSONL line is not an AOP event: %s", err)
		}
		counts[aop.Kind(event)]++
		if extension := event.GetExtension(); extension != nil && extension.MessageIs(&artifact) {
			if err := extension.UnmarshalTo(&artifact); err != nil {
				t.Fatalf("decode artifact: %v", err)
			}
			if found, err := aop.FindTypedExtension(event, &artifactRef); err != nil || !found {
				t.Fatalf("decode artifact correlation: found=%v err=%v", found, err)
			}
		}
	}
	if err := scanner.Err(); err != nil {
		t.Fatalf("read JSONL: %v", err)
	}
	if counts["tool.call"] != 1 || counts["tool.result"] != 1 || counts["aop.tool.Artifact"] != 1 {
		t.Fatalf("event counts = %#v", counts)
	}
	if artifact.Tool != "gogo" || artifact.Kind != toolpb.ArtifactKindService || artifact.Target != "127.0.0.1:443" || artifactRef.GetCallId() != "call-1" {
		t.Fatalf("artifact = %#v", &artifact)
	}
	var decoded parsers.GOGOResult
	if err := json.Unmarshal(artifact.Data, &decoded); err != nil {
		t.Fatalf("decode gogo result: %v", err)
	}
	if decoded.Ip != "127.0.0.1" || decoded.Port != "443" || decoded.Protocol != "https" {
		t.Fatalf("gogo result = %#v", decoded)
	}
}
