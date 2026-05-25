package swarm

import (
	"context"
	"net/http/httptest"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/ioa"
	ioaclient "github.com/chainreactors/ioa/client"
	ioaserver "github.com/chainreactors/ioa/server"
)

func TestRealLLMSwarmNodeRepliesThroughIOA(t *testing.T) {
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

	worker, err := ioaclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()

	systemPrompt := "You are a concise test worker. When asked to reply, answer with exactly: loop"
	node := NewNode(NodeConfig{
		Client:           worker,
		NodeName:         "real-llm-worker",
		SpaceName:        "default",
		SpaceDescription: "real llm worker",
		PollInterval:     100 * time.Millisecond,
		Prompt:           "reply loop to hello",
		Intent:           "reply loop to hello",
		Network:          map[string]any{"test": "real-llm"},
		OnTask: func(ctx context.Context, task Task) (string, error) {
			resp, err := llmProvider.ChatCompletion(ctx, &provider.ChatCompletionRequest{
				Model: model,
				Messages: []provider.ChatMessage{
					{Role: "system", Content: strPtr(systemPrompt)},
					{Role: "user", Content: strPtr(task.Content)},
				},
			})
			if err != nil {
				return "", err
			}
			if len(resp.Choices) > 0 && resp.Choices[0].Message.Content != nil {
				return *resp.Choices[0].Message.Content, nil
			}
			return "", nil
		},
	})
	go func() {
		_ = node.Run(runCtx)
	}()

	controller, err := ioaclient.NewClient(server.URL, "")
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
			"content": "Reply with exactly one word: loop",
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
			c, _ := msg.Content["content"].(string)
			if strings.Contains(strings.ToLower(c), "loop") && containsRef(msg.Refs.Messages, hello.ID) {
				return
			}
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for real LLM loop reply; messages=%d", len(related))
		default:
			time.Sleep(500 * time.Millisecond)
		}
	}
}

func strPtr(s string) *string { return &s }
