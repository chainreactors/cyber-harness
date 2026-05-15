package loop

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/protocol"
	"github.com/chainreactors/aiscan/pkg/provider"
	"github.com/chainreactors/ioa"
	ioaclient "github.com/chainreactors/ioa/client"
	ioaserver "github.com/chainreactors/ioa/server"
)

func TestThreeLoopClientsCollaborateThroughIOA(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore())
	server := httptest.NewServer(ioaserver.NewHandler(service))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	controller, err := ioaclient.NewClient(server.URL, "")
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

	workerClients := make([]*ioaclient.Client, 3)
	workerNodes := make([]ioa.Node, 3)
	providers := make([]*taskProvider, 3)
	for i := 0; i < 3; i++ {
		client, err := ioaclient.NewClient(server.URL, "")
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

	// Send tasks using SwarmMessage format
	for i, node := range workerNodes {
		_, err := controller.Send(ctx, space.ID, ioa.SendMessage{
			Content: map[string]any{
				"content": fmt.Sprintf("task-%d", i+1),
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
		reports := countReports(all)
		accepts := countAccepts(all)
		if reports == 3 && accepts == 3 {
			for i, p := range providers {
				if got := p.tasks(); len(got) != 1 || got[0] != fmt.Sprintf("task-%d", i+1) {
					t.Fatalf("provider %d tasks = %#v", i+1, got)
				}
			}
			for _, msg := range all {
				c, _ := msg.Content["content"].(string)
				if msg.Sender == controllerNode.ID && strings.Contains(c, "completed task-") {
					t.Fatal("controller should not send result messages")
				}
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for collaboration; reports=%d accepts=%d messages=%d", reports, accepts, len(all))
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

	controller, err := ioaclient.NewClient(server.URL, "")
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
		client, err := ioaclient.NewClient(server.URL, "")
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
			"content": "hello",
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
		replies := countRepliesWithContent(related, hello.ID, "loop")
		accepts := countAccepts(related)
		if replies == 3 && accepts == 3 {
			for i, p := range providers {
				if got := p.tasks(); len(got) != 1 || got[0] != "hello" {
					t.Fatalf("provider %d tasks = %#v", i+1, got)
				}
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for hello replies; replies=%d accepts=%d messages=%d", replies, accepts, len(related))
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestLoopAnnouncesSwarmProfile(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore())
	server := httptest.NewServer(ioaserver.NewHandler(service))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	client, err := ioaclient.NewClient(server.URL, "")
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
			"hostname": "test-host",
		},
	})
	go func() {
		_ = runner.Run(runCtx)
	}()

	controller, err := ioaclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.RegisterNode(ctx, "controller", nil); err != nil {
		t.Fatal(err)
	}
	deadline := time.After(5 * time.Second)
	for {
		space, err := controller.Space(ctx, "default", "controller")
		if err != nil {
			t.Fatal(err)
		}
		messages, err := controller.Read(ctx, space.ID, ioa.ReadOptions{All: true})
		if err != nil {
			t.Fatal(err)
		}
		if profile := findAnnounce(messages); profile != nil {
			if len(profile.Refs.Messages) != 0 || len(profile.Refs.Nodes) != 0 {
				t.Fatalf("profile refs = %#v, want empty", profile.Refs)
			}
			content, _ := profile.Content["content"].(string)
			if !strings.Contains(content, "joined the swarm") {
				t.Fatalf("announce content missing 'joined the swarm': %q", content)
			}
			if !strings.Contains(content, "scan localhost") {
				t.Fatalf("announce content missing intent: %q", content)
			}
			meta, ok := profile.Content["meta"].(map[string]any)
			if !ok {
				t.Fatalf("announce missing meta: %#v", profile.Content)
			}
			if meta["hostname"] != "test-host" {
				t.Fatalf("meta hostname = %v, want test-host", meta["hostname"])
			}
			caps, ok := meta["capabilities"].([]any)
			if !ok || len(caps) != 2 {
				t.Fatalf("meta capabilities = %#v", meta["capabilities"])
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for announce; messages=%d", len(messages))
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestLoopHeartbeatRunsAgent(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore())
	server := httptest.NewServer(ioaserver.NewHandler(service))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	controller, err := ioaclient.NewClient(server.URL, "")
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
			"content": "existing context note",
		},
	})
	if err != nil {
		t.Fatal(err)
	}

	worker, err := ioaclient.NewClient(server.URL, "")
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
			c, _ := msg.Content["content"].(string)
			if c != "heartbeat done" {
				continue
			}
			tasks := llm.tasks()
			if len(tasks) == 0 {
				t.Fatal("provider did not receive heartbeat prompt")
			}
			prompt := tasks[len(tasks)-1]
			for _, want := range []string{"Swarm heartbeat", space.ID, note.ID, "existing context", "watch the case"} {
				if !strings.Contains(prompt, want) {
					t.Fatalf("heartbeat prompt missing %q:\n%s", want, prompt)
				}
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for heartbeat; messages=%d", len(all))
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestSwarmFromIOAParsesNewAndLegacy(t *testing.T) {
	// New format: content field
	msg := ioa.Message{Content: map[string]any{"content": "scan this"}}
	sm, ok := swarmFromIOA(msg)
	if !ok || sm.Content != "scan this" {
		t.Fatalf("swarmFromIOA(new) = %#v, %v", sm, ok)
	}

	// New format with targets + meta
	msg = ioa.Message{Content: map[string]any{
		"content": "full scan",
		"targets": []any{"10.0.0.0/24"},
		"meta":    map[string]any{"ip": "10.0.0.5"},
	}}
	sm, ok = swarmFromIOA(msg)
	if !ok || sm.Content != "full scan" || len(sm.Targets) != 1 || sm.Meta["ip"] != "10.0.0.5" {
		t.Fatalf("swarmFromIOA(new+targets+meta) = %#v, %v", sm, ok)
	}

	// Legacy format: task field
	msg = ioa.Message{Content: map[string]any{"task": "legacy task"}}
	sm, ok = swarmFromIOA(msg)
	if !ok || sm.Content != "legacy task" {
		t.Fatalf("swarmFromIOA(legacy task) = %#v, %v", sm, ok)
	}

	// Legacy format: prompt field
	msg = ioa.Message{Content: map[string]any{"prompt": "legacy prompt"}}
	sm, ok = swarmFromIOA(msg)
	if !ok || sm.Content != "legacy prompt" {
		t.Fatalf("swarmFromIOA(legacy prompt) = %#v, %v", sm, ok)
	}

	// Non-parseable: no content/task/prompt
	msg = ioa.Message{Content: map[string]any{"type": "note", "text": "hello"}}
	_, ok = swarmFromIOA(msg)
	if ok {
		t.Fatal("swarmFromIOA should reject messages without content/task/prompt")
	}
}

// ── Helpers ─────────────────────────────────────────────────────────

type taskProvider struct {
	name  string
	reply string

	mu    sync.Mutex
	seen  []string
	calls int
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

func findAnnounce(messages []ioa.Message) *ioa.Message {
	for i := range messages {
		c, _ := messages[i].Content["content"].(string)
		if strings.Contains(c, "joined the swarm") {
			return &messages[i]
		}
	}
	return nil
}

func countReports(messages []ioa.Message) int {
	count := 0
	for _, msg := range messages {
		c, _ := msg.Content["content"].(string)
		if strings.Contains(c, "completed task-") && len(msg.Refs.Messages) > 0 {
			count++
		}
	}
	return count
}

func countAccepts(messages []ioa.Message) int {
	count := 0
	for _, msg := range messages {
		c, _ := msg.Content["content"].(string)
		if strings.Contains(c, "Accepted task") {
			count++
		}
	}
	return count
}

func countRepliesWithContent(messages []ioa.Message, parentID, want string) int {
	count := 0
	for _, msg := range messages {
		c, _ := msg.Content["content"].(string)
		if c == want && containsRef(msg.Refs.Messages, parentID) {
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

// Verify protocol.SwarmMessage round-trips correctly
func TestSwarmContentRoundTrip(t *testing.T) {
	msg := protocol.SwarmMessage{
		Content: "scan these targets",
		Targets: []string{"10.0.0.0/24", "192.168.1.0/24"},
		Meta:    map[string]any{"ip": "10.0.0.5", "hostname": "scanner-1"},
	}
	raw := swarmContent(msg)
	parsed, ok := protocol.ParseSwarm(raw)
	if !ok {
		t.Fatal("ParseSwarm failed on round-trip")
	}
	if parsed.Content != msg.Content {
		t.Fatalf("content = %q, want %q", parsed.Content, msg.Content)
	}
	if len(parsed.Targets) != 2 {
		t.Fatalf("targets = %v, want 2 items", parsed.Targets)
	}
	if parsed.Meta["ip"] != "10.0.0.5" {
		t.Fatalf("meta.ip = %v, want 10.0.0.5", parsed.Meta["ip"])
	}
}
