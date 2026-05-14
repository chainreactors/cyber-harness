package loop

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/provider"
	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/ioa"
	acpclient "github.com/chainreactors/ioa/client"
	ioaserver "github.com/chainreactors/ioa/server"
)

func TestThreeLoopClientsCollaborateThroughACP(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore())
	server := httptest.NewServer(ioaserver.NewHandler(service))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	controller, err := acpclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	controllerNode, err := controller.RegisterNode(ctx, "controller", nil)
	if err != nil {
		t.Fatal(err)
	}
	space, err := controller.Space(ctx, "case-e2e", "manual task sender")
	if err != nil {
		t.Fatal(err)
	}

	workerClients := make([]*acpclient.Client, 3)
	workerNodes := make([]ioa.Node, 3)
	providers := make([]*taskProvider, 3)
	for i := 0; i < 3; i++ {
		client, err := acpclient.NewClient(server.URL, "")
		if err != nil {
			t.Fatal(err)
		}
		node, err := client.RegisterNode(ctx, fmt.Sprintf("worker-%d", i+1), nil)
		if err != nil {
			t.Fatal(err)
		}
		if _, err := client.Space(ctx, "case-e2e", fmt.Sprintf("worker %d", i+1)); err != nil {
			t.Fatal(err)
		}
		workerClients[i] = client
		workerNodes[i] = node
		providers[i] = &taskProvider{name: node.Name}
	}

	runCtx, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()
	for i := 0; i < 3; i++ {
		runner := New(Config{
			Client:           workerClients[i],
			Provider:         providers[i],
			Tools:            command.NewRegistry(),
			SystemPrompt:     "test loop agent",
			Model:            "test-model",
			NodeName:         workerNodes[i].Name,
			SpaceName:        "case-e2e",
			SpaceDescription: "worker",
			PollInterval:     100 * time.Millisecond,
			Network:          map[string]any{"test": true},
		})
		go func() {
			_ = runner.Run(runCtx)
		}()
	}

	for i, node := range workerNodes {
		_, err := controller.Send(ctx, space.ID, ioa.SendMessage{
			Content: map[string]any{
				"type": "task",
				"task": fmt.Sprintf("task-%d", i+1),
			},
			Refs: &ioa.Ref{Nodes: []string{node.ID}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	deadline := time.After(5 * time.Second)
	for {
		all, err := controller.Read(ctx, space.ID, ioa.ReadOptions{All: true})
		if err != nil {
			t.Fatal(err)
		}
		if countResults(all) == 3 && countStatus(all, "started") == 3 {
			for i, p := range providers {
				if got := p.tasks(); len(got) != 1 || got[0] != fmt.Sprintf("task-%d", i+1) {
					t.Fatalf("provider %d tasks = %#v", i+1, got)
				}
			}
			for _, msg := range all {
				if msg.Sender == controllerNode.ID && msg.Content["type"] == "result" {
					t.Fatal("controller should not send result messages")
				}
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for collaboration; messages=%#v", all)
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestThreeLoopClientsReplyToBroadcastHello(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore())
	server := httptest.NewServer(ioaserver.NewHandler(service))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	controller, err := acpclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.RegisterNode(ctx, "controller", nil); err != nil {
		t.Fatal(err)
	}
	space, err := controller.Space(ctx, "default", "manual task sender")
	if err != nil {
		t.Fatal(err)
	}

	providers := make([]*taskProvider, 3)
	runCtx, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()
	for i := 0; i < 3; i++ {
		client, err := acpclient.NewClient(server.URL, "")
		if err != nil {
			t.Fatal(err)
		}
		providers[i] = &taskProvider{name: fmt.Sprintf("worker-%d", i+1), reply: "loop"}
		runner := New(Config{
			Client:           client,
			Provider:         providers[i],
			Tools:            command.NewRegistry(),
			SystemPrompt:     "test loop agent",
			Model:            "test-model",
			NodeName:         providers[i].name,
			SpaceName:        "default",
			SpaceDescription: "worker",
			PollInterval:     100 * time.Millisecond,
			Intent:           "reply loop to hello",
			Skills:           []string{"aiscan"},
			Network:          map[string]any{"cidr": "127.0.0.0/8"},
		})
		go func() {
			_ = runner.Run(runCtx)
		}()
	}

	hello, err := controller.Send(ctx, space.ID, ioa.SendMessage{
		Content: map[string]any{
			"type": "task",
			"task": "hello",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	deadline := time.After(5 * time.Second)
	for {
		related, err := controller.Read(ctx, space.ID, ioa.ReadOptions{MessageID: hello.ID})
		if err != nil {
			t.Fatal(err)
		}
		if countHelloResults(related, hello.ID) == 3 && countStatus(related, "started") == 3 {
			for i, p := range providers {
				if got := p.tasks(); len(got) != 1 || got[0] != "hello" {
					t.Fatalf("provider %d tasks = %#v", i+1, got)
				}
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for hello replies; messages=%#v", related)
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestLoopAnnouncesNodeProfile(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore())
	server := httptest.NewServer(ioaserver.NewHandler(service))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := acpclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	runner := New(Config{
		Client:           client,
		Provider:         &taskProvider{name: "worker-profile"},
		Tools:            command.NewRegistry(),
		SystemPrompt:     "test loop agent",
		Model:            "test-model",
		NodeName:         "worker-profile",
		SpaceName:        "default",
		SpaceDescription: "profile worker",
		PollInterval:     100 * time.Millisecond,
		Intent:           "scan localhost",
		Prompt:           "scan localhost",
		Skills:           []string{"aiscan", "scan"},
		Network: map[string]any{
			"hostname":   "test-host",
			"interfaces": []string{"lo"},
		},
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
	var space ioa.SpaceInfo
	deadline := time.After(5 * time.Second)
	for {
		space, err = controller.Space(ctx, "default", "controller")
		if err != nil {
			t.Fatal(err)
		}
		messages, err := controller.Read(ctx, space.ID, ioa.ReadOptions{All: true})
		if err != nil {
			t.Fatal(err)
		}
		if profile := findProfile(messages); profile != nil {
			if len(profile.Refs.Messages) != 0 || len(profile.Refs.Nodes) != 0 {
				t.Fatalf("profile refs = %#v, want empty", profile.Refs)
			}
			if profile.Content["type"] != "node_profile" || profile.Content["intent"] != "scan localhost" || profile.Content["prompt"] != "scan localhost" {
				t.Fatalf("unexpected profile content: %#v", profile.Content)
			}
			skills, ok := profile.Content["skills"].([]any)
			if !ok || len(skills) != 2 || skills[0] != "aiscan" || skills[1] != "scan" {
				t.Fatalf("profile skills = %#v", profile.Content["skills"])
			}
			network, ok := profile.Content["network"].(map[string]any)
			if !ok || network["hostname"] != "test-host" {
				t.Fatalf("profile network = %#v", profile.Content["network"])
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for node profile; messages=%#v", messages)
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestLoopHeartbeatRunsAgentWithACPContext(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore())
	server := httptest.NewServer(ioaserver.NewHandler(service))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	controller, err := acpclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.RegisterNode(ctx, "controller", nil); err != nil {
		t.Fatal(err)
	}
	space, err := controller.Space(ctx, "heartbeat-case", "controller")
	if err != nil {
		t.Fatal(err)
	}
	note, err := controller.Send(ctx, space.ID, ioa.SendMessage{
		Content: map[string]any{
			"type": "note",
			"text": "existing context",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	worker, err := acpclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	llm := &taskProvider{name: "heartbeat-worker", reply: "heartbeat done"}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	runner := New(Config{
		Client:                worker,
		Provider:              llm,
		Tools:                 command.NewRegistry(),
		SystemPrompt:          "test loop agent",
		Model:                 "test-model",
		NodeName:              "heartbeat-worker",
		SpaceName:             "heartbeat-case",
		SpaceDescription:      "worker",
		PollInterval:          100 * time.Millisecond,
		HeartbeatInterval:     50 * time.Millisecond,
		HeartbeatContextLimit: 20,
		Prompt:                "watch the case",
		Network:               map[string]any{"test": true},
	})
	go func() {
		_ = runner.Run(runCtx)
	}()

	deadline := time.After(5 * time.Second)
	for {
		all, err := controller.Read(ctx, space.ID, ioa.ReadOptions{All: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, msg := range all {
			if msg.Content["type"] != "heartbeat_result" || msg.Content["status"] != "done" {
				continue
			}
			output, _ := msg.Content["output"].(string)
			if output != "heartbeat done" {
				t.Fatalf("heartbeat output = %q", output)
			}
			if len(msg.Refs.Messages) != 1 || !hasMessageWithType(all, msg.Refs.Messages[0], "heartbeat") {
				t.Fatalf("heartbeat result refs = %#v; messages=%#v", msg.Refs, all)
			}
			tasks := llm.tasks()
			if len(tasks) == 0 {
				t.Fatal("provider did not receive heartbeat prompt")
			}
			prompt := tasks[len(tasks)-1]
			for _, want := range []string{"IOA heartbeat", space.ID, note.ID, "existing context", "watch the case"} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("heartbeat prompt missing %q:\n%s", want, prompt)
				}
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for heartbeat; messages=%#v", all)
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestTaskFromMessageIgnoresTypedNonTasks(t *testing.T) {
	if task, ok := taskFromMessage(ioa.Message{Content: map[string]any{
		"type":   "node_profile",
		"prompt": "worker intent",
	}}); ok || task != "" {
		t.Fatalf("taskFromMessage() = %q, %v; want no task", task, ok)
	}
	if task, ok := taskFromMessage(ioa.Message{Content: map[string]any{
		"type": "heartbeat",
		"task": "not a task",
	}}); ok || task != "" {
		t.Fatalf("taskFromMessage() = %q, %v; want no task", task, ok)
	}
	if task, ok := taskFromMessage(ioa.Message{Content: map[string]any{
		"prompt": "legacy prompt",
	}}); !ok || task != "legacy prompt" {
		t.Fatalf("taskFromMessage() = %q, %v; want legacy prompt", task, ok)
	}
	if task, ok := taskFromMessage(ioa.Message{Content: map[string]any{
		"type":    "task",
		"content": "typed task",
	}}); !ok || task != "typed task" {
		t.Fatalf("taskFromMessage() = %q, %v; want typed task", task, ok)
	}
}

type taskProvider struct {
	name  string
	reply string

	mu    sync.Mutex
	seen  []string
	calls int
}

func findProfile(messages []ioa.Message) *ioa.Message {
	for i := range messages {
		if messages[i].Content["type"] == "node_profile" {
			return &messages[i]
		}
	}
	return nil
}

func (p *taskProvider) Name() string { return p.name }

func (p *taskProvider) ChatCompletion(_ context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.calls++
	task := lastUserContent(req.Messages)
	p.seen = append(p.seen, task)
	reply := p.reply
	if reply == "" {
		reply = fmt.Sprintf("%s completed %s", p.name, task)
	}
	return &provider.ChatCompletionResponse{
		Choices: []provider.Choice{{
			Message: provider.NewTextMessage("assistant", reply),
		}},
	}, nil
}

func (p *taskProvider) tasks() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.seen...)
}

func lastUserContent(messages []provider.ChatMessage) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" && messages[i].Content != nil {
			return *messages[i].Content
		}
	}
	return ""
}

func countResults(messages []ioa.Message) int {
	count := 0
	for _, msg := range messages {
		if msg.Content["type"] == "result" && msg.Content["status"] == "done" {
			output, _ := msg.Content["output"].(string)
			if strings.Contains(output, "completed task-") {
				count++
			}
		}
	}
	return count
}

func countHelloResults(messages []ioa.Message, helloID string) int {
	count := 0
	for _, msg := range messages {
		if msg.Content["type"] == "result" && msg.Content["status"] == "done" && containsRef(msg.Refs.Messages, helloID) {
			output, _ := msg.Content["output"].(string)
			if output == "loop" {
				count++
			}
		}
	}
	return count
}

func countStatus(messages []ioa.Message, status string) int {
	count := 0
	for _, msg := range messages {
		if msg.Content["type"] == "status" && msg.Content["status"] == status {
			count++
		}
	}
	return count
}

func containsRef(values []string, want string) bool {
	for _, value := range values {
		if value == want {
			return true
		}
	}
	return false
}

func hasMessageWithType(messages []ioa.Message, id, typ string) bool {
	for _, msg := range messages {
		if msg.ID == id && msg.Content["type"] == typ {
			return true
		}
	}
	return false
}
