package webagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"sync"
	"testing"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/runner"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

type uploadCaptureProvider struct {
	mu       sync.Mutex
	messages []agent.ChatMessage
}

func (p *uploadCaptureProvider) Name() string { return "upload-capture" }

func (p *uploadCaptureProvider) ChatCompletion(_ context.Context, req *agent.ChatCompletionRequest) (*agent.ChatCompletionResponse, error) {
	p.mu.Lock()
	p.messages = append([]agent.ChatMessage(nil), req.Messages...)
	p.mu.Unlock()
	return &agent.ChatCompletionResponse{Choices: []agent.Choice{{Message: agent.NewTextMessage("assistant", "done")}}}, nil
}

func (p *uploadCaptureProvider) contains(text string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	for _, message := range p.messages {
		if message.Content != nil && strings.Contains(*message.Content, text) {
			return true
		}
	}
	return false
}

// handleFileUpload must write the bytes to the agent's local disk AND queue a note
// carrying that absolute path for the session's next turn — the fix for the LLM
// only ever seeing the hub's UI-only "file uploaded" notice and then guessing a
// bare filename against its cwd.
func TestHandleFileUploadRecordsAbsolutePathForNextTurn(t *testing.T) {
	rt, err := runner.NewAgentRuntime(context.Background(), &cfg.Option{}, telemetry.NopLogger(), &runner.RuntimeConfig{
		NoOutput:         true,
		ProviderOptional: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	defer rt.Close()
	provider := &uploadCaptureProvider{}
	rt.SetProvider(provider, agent.ProviderConfig{Provider: provider.Name(), Model: "test"})

	const filename = "aiscan_test_upload_probe.txt"
	const body = "codex public proof\nkey=appImage/probe"
	dest := filepath.Join(os.TempDir(), "aiscan-uploads", filename)
	t.Cleanup(func() { _ = os.Remove(dest) })

	payload, _ := json.Marshal(webproto.FileUploadPayload{Filename: filename, SessionID: "sess-1"})
	msg := webproto.Message{
		Type:    "upload",
		TaskID:  "task-1",
		DataB64: base64.StdEncoding.EncodeToString([]byte(body)),
		Payload: payload,
	}

	var got webproto.Message
	handleFileUpload(msg, func(out webproto.Message) { got = out }, rt)

	// The agent replied with the written path and no error.
	var res webproto.FileUploadResult
	if err := json.Unmarshal(got.Payload, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected upload error: %s", res.Error)
	}
	if res.Path != dest {
		t.Fatalf("result path = %q, want %q", res.Path, dest)
	}

	// The bytes actually landed on disk.
	if data, err := os.ReadFile(dest); err != nil || string(data) != body {
		t.Fatalf("file on disk = %q, err=%v; want %q", data, err, body)
	}

	event := aop.Event{
		Type:      aop.TypeMessage,
		TS:        "2026-07-22T00:00:00Z",
		SessionID: "sess-1",
		Agent:     "test",
		Data: webproto.MustJSON(aop.MessageData{
			MessageID: "m-1",
			Role:      "user",
			Parts:     []aop.MessagePart{{Type: aop.PartText, Text: "read the uploaded file"}},
		}),
	}
	inbound, err := agent.Classify(event)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := rt.Execute(context.Background(), "request-1", inbound, nil); err != nil {
		t.Fatal(err)
	}
	if !provider.contains(dest) || !provider.contains(filename) {
		t.Fatalf("provider request did not receive upload note for %q", dest)
	}
}
