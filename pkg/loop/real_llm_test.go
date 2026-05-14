package loop

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/provider"
	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/ioa"
	acpclient "github.com/chainreactors/ioa/client"
	ioaserver "github.com/chainreactors/ioa/server"
)

func TestRealLLMLoopRepliesThroughACP(t *testing.T) {
	if os.Getenv("AISCAN_REAL_LLM") != "1" {
		t.Skip("set AISCAN_REAL_LLM=1 to run real LLM integration test")
	}
	apiKey := strings.TrimSpace(os.Getenv("AISCAN_API_KEY"))
	if apiKey == "" {
		apiKey = strings.TrimSpace(os.Getenv("DEEPSEEK_API_KEY"))
	}
	if apiKey == "" {
		t.Skip("set AISCAN_API_KEY or DEEPSEEK_API_KEY to run real LLM integration test")
	}
	baseURL := strings.TrimSpace(os.Getenv("AISCAN_BASE_URL"))
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	model := strings.TrimSpace(os.Getenv("AISCAN_MODEL"))
	if model == "" {
		model = "deepseek-v4-pro"
	}

	service := ioaserver.NewService(ioaserver.NewMemoryStore())
	server := httptest.NewServer(ioaserver.NewHandler(service))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	llmProvider, err := provider.NewProvider(&provider.ProviderConfig{
		Provider: "openai",
		BaseURL:  baseURL,
		APIKey:   apiKey,
		Model:    model,
		Timeout:  60,
	})
	if err != nil {
		t.Fatal(err)
	}
	worker, err := acpclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	runner := New(Config{
		Client:           worker,
		Provider:         llmProvider,
		Tools:            command.NewRegistry(),
		SystemPrompt:     "You are a concise test worker. When asked to reply, answer with exactly: loop",
		Model:            model,
		Stream:           false,
		NodeName:         "real-llm-worker",
		SpaceName:        "default",
		SpaceDescription: "real llm worker",
		PollInterval:     100 * time.Millisecond,
		Prompt:           "reply loop to hello",
		Intent:           "reply loop to hello",
		Network:          map[string]any{"test": "real-llm"},
	})
	go func() {
		_ = runner.Run(runCtx)
	}()

	controller, err := acpclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.RegisterNode(ctx, "controller", nil); err != nil {
		t.Fatal(err)
	}
	space, err := controller.Space(ctx, "default", "controller")
	if err != nil {
		t.Fatal(err)
	}
	hello, err := controller.Send(ctx, space.ID, ioa.SendMessage{
		Content: map[string]any{
			"type": "task",
			"task": "Reply with exactly one word: loop",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.After(90 * time.Second)
	for {
		related, err := controller.Read(ctx, space.ID, ioa.ReadOptions{MessageID: hello.ID})
		if err != nil {
			t.Fatal(err)
		}
		for _, msg := range related {
			if msg.Content["type"] != "result" || msg.Content["status"] != "done" {
				continue
			}
			output, _ := msg.Content["output"].(string)
			if strings.Contains(strings.ToLower(output), "loop") && containsRef(msg.Refs.Messages, hello.ID) {
				return
			}
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for real LLM loop reply; messages=%#v", related)
		default:
			time.Sleep(500 * time.Millisecond)
		}
	}
}
