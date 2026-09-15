package app

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"

	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/pkg/commands"
	telemetryext "github.com/chainreactors/cyber/pkg/exts/telemetry"
	"github.com/chainreactors/cyber/pkg/toolset"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent"
	providerapi "github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/utils/parsers"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/anypb"
)

func TestAppUsesProfileRegistriesWithoutOwningThem(t *testing.T) {
	hookRegistry := hooks.New()
	commandRegistry := commands.NewRegistry(hookRegistry)
	toolRegistry := toolset.NewRegistry(hookRegistry)
	resource := newTestApp(t, Config{SkipEngines: true, Logger: telemetry.NopLogger()}, AppServices{
		Hooks: hookRegistry, Commands: commandRegistry, Tools: toolRegistry,
	})
	application := resource.App
	if application.Commands != commandRegistry || application.Tools != toolRegistry || application.Hooks != hookRegistry {
		t.Fatal("application replaced profile-owned registries")
	}
}

func TestLogLLMProbeStatusReady(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"id": "probe-1",
			"choices": []map[string]any{
				{"message": map[string]any{"role": "assistant", "content": "pong"}, "finish_reason": "stop"},
			},
		})
	}))
	defer srv.Close()

	var logBuf bytes.Buffer
	logger := telemetry.NewLogger(telemetry.LogConfig{Debug: true, Output: &logBuf})

	health := providerapi.Probe(context.Background(), agent.ProviderConfig{
		Provider: "openai",
		BaseURL:  srv.URL + "/v1",
		APIKey:   "sk-test",
		Model:    "gpt-test",
	}, logger)
	if health.State != LLMHealthReady || health.LatencyMs < 0 || health.Error != "" {
		t.Fatalf("health = %+v", health)
	}

	logText := logBuf.String()
	if !strings.Contains(logText, "● llm") ||
		!strings.Contains(logText, "openai/gpt-test") ||
		!strings.Contains(logText, "ms") {
		t.Fatalf("missing ready probe log:\n%s", logText)
	}
}

func TestLogLLMProbeStatusUnready(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()

	var logBuf bytes.Buffer
	logger := telemetry.NewLogger(telemetry.LogConfig{Output: &logBuf})

	health := providerapi.Probe(context.Background(), agent.ProviderConfig{
		Provider: "openai",
		BaseURL:  srv.URL + "/v1",
		APIKey:   "sk-test",
		Model:    "gpt-test",
	}, logger)
	if health.State != LLMHealthFailed || !strings.Contains(health.Error, "unauthorized") {
		t.Fatalf("health = %+v", health)
	}

	logText := logBuf.String()
	if !strings.Contains(logText, "● fail llm") ||
		!strings.Contains(logText, "openai/gpt-test") ||
		!strings.Contains(logText, "ms") ||
		!strings.Contains(logText, "unauthorized") {
		t.Fatalf("missing unready probe log:\n%s", logText)
	}
}

func TestAppLoggerCanBeRetargeted(t *testing.T) {
	var first, second bytes.Buffer
	app := &App{}
	app.SetLogger(telemetry.NewLogger(telemetry.LogConfig{Debug: true, Output: &first}))
	logger := app.Logger()

	logger.Infof("before")
	app.SetLogger(telemetry.NewLogger(telemetry.LogConfig{Debug: true, Output: &second}))
	logger.Infof("after")

	if !strings.Contains(first.String(), "before") {
		t.Fatalf("initial logger missing: %q", first.String())
	}
	if strings.Contains(first.String(), "after") {
		t.Fatalf("retargeted log went to old writer: %q", first.String())
	}
	if !strings.Contains(second.String(), "after") {
		t.Fatalf("retargeted logger missing: %q", second.String())
	}
}

func TestJSONLRecorderPersistsCanonicalEventsAndOneArtifactPerResult(t *testing.T) {
	path := filepath.Join(t.TempDir(), "session.jsonl")
	events := coreevents.New()
	recorder, err := telemetryext.New(events, telemetryext.Options{Path: path})
	if err != nil {
		t.Fatal(err)
	}
	appResource := newTestApp(t, Config{SkipEngines: true, Logger: telemetry.NopLogger()}, AppServices{Events: events})
	app := appResource.App
	appSet := testSet(t,
		extension.Entry{ID: "output", Extension: recorder},
		extension.Entry{ID: "app", DependsOn: []string{"output"}, Extension: appResource},
	)
	if err := appSet.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	app.Publish(&aop.Event{
		SessionId: "session-1", TurnId: "turn-1", Emitter: "cyber",
		Payload: &aop.Event_ToolCall{ToolCall: &aop.ToolCall{Id: "call-1", Name: "gogo"}},
	})
	app.Progress.Emit(&toolpb.Progress{Tool: "gogo", Text: "raw PTY bytes", CallId: "call-1"})
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
