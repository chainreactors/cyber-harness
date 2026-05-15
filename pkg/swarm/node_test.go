package swarm

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/ioa"
	ioaclient "github.com/chainreactors/ioa/client"
	ioaserver "github.com/chainreactors/ioa/server"
)

func TestThreeSwarmNodesCollaborate(t *testing.T) {
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
	space, err := controller.Space(ctx, "case-e2e", "manual task sender")
	if err != nil {
		t.Fatal(err)
	}

	workerClients := make([]*ioaclient.Client, 3)
	workerNodes := make([]ioa.Node, 3)
	handlers := make([]*taskRecorder, 3)
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
		handlers[i] = &taskRecorder{name: node.Name}
	}

	runCtx, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()
	for i := 0; i < 3; i++ {
		h := handlers[i]
		node := NewNode(NodeConfig{
			Client:           workerClients[i],
			NodeName:         workerNodes[i].Name,
			SpaceName:        "case-e2e",
			SpaceDescription: "worker",
			PollInterval:     100 * time.Millisecond,
			Network:          map[string]any{"test": true},
			OnTask: func(ctx context.Context, task Task) (string, error) {
				h.record(task.Content)
				return fmt.Sprintf("%s completed %s", h.name, task.Content), nil
			},
		})
		go func() {
			_ = node.Run(runCtx)
		}()
	}

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
			for i, h := range handlers {
				if got := h.tasks(); len(got) != 1 || got[0] != fmt.Sprintf("task-%d", i+1) {
					t.Fatalf("handler %d tasks = %#v", i+1, got)
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

func TestThreeSwarmNodesReplyToBroadcastHello(t *testing.T) {
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

	handlers := make([]*taskRecorder, 3)
	runCtx, stopWorkers := context.WithCancel(ctx)
	defer stopWorkers()
	for i := 0; i < 3; i++ {
		client, err := ioaclient.NewClient(server.URL, "")
		if err != nil {
			t.Fatal(err)
		}
		handlers[i] = &taskRecorder{name: fmt.Sprintf("worker-%d", i+1)}
		h := handlers[i]
		node := NewNode(NodeConfig{
			Client:           client,
			NodeName:         h.name,
			SpaceName:        "default",
			SpaceDescription: "worker",
			PollInterval:     100 * time.Millisecond,
			Intent:           "reply loop to hello",
			Skills:           []string{"aiscan"},
			Network:          map[string]any{"cidr": "127.0.0.0/8"},
			OnTask: func(ctx context.Context, task Task) (string, error) {
				h.record(task.Content)
				return "loop", nil
			},
		})
		go func() {
			_ = node.Run(runCtx)
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
			for i, h := range handlers {
				if got := h.tasks(); len(got) != 1 || got[0] != "hello" {
					t.Fatalf("handler %d tasks = %#v", i+1, got)
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

func TestNodeAnnouncesSwarmProfile(t *testing.T) {
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
	node := NewNode(NodeConfig{
		Client:           client,
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
		OnTask: func(ctx context.Context, task Task) (string, error) {
			return "ok", nil
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
			if node.RootMessageID() == "" {
				t.Fatal("RootMessageID() should be non-empty after announceProfile")
			}
			if node.RootMessageID() != profile.ID {
				t.Fatalf("RootMessageID() = %q, want %q", node.RootMessageID(), profile.ID)
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

func TestNodeHeartbeatRunsHandler(t *testing.T) {
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
	rec := &taskRecorder{name: "heartbeat-worker"}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	node := NewNode(NodeConfig{
		Client:                worker,
		NodeName:              "heartbeat-worker",
		SpaceName:             "heartbeat-case",
		SpaceDescription:      "worker",
		PollInterval:          100 * time.Millisecond,
		HeartbeatInterval:     50 * time.Millisecond,
		HeartbeatContextLimit: 20,
		Prompt:                "watch the case",
		Network:               map[string]any{"test": true},
		OnTask: func(ctx context.Context, task Task) (string, error) {
			return "ok", nil
		},
		OnHeartbeat: func(ctx context.Context, prompt string) (string, error) {
			rec.record(prompt)
			return "heartbeat done", nil
		},
	})
	go func() {
		_ = node.Run(runCtx)
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
			tasks := rec.tasks()
			if len(tasks) == 0 {
				t.Fatal("handler did not receive heartbeat prompt")
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

func TestNodeAcceptsTaskByRootMessageRef(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore())
	server := httptest.NewServer(ioaserver.NewHandler(service))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	worker, err := ioaclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	rec := &taskRecorder{name: "root-msg-worker"}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	node := NewNode(NodeConfig{
		Client:           worker,
		NodeName:         "root-msg-worker",
		SpaceName:        "root-msg-test",
		SpaceDescription: "worker",
		PollInterval:     100 * time.Millisecond,
		Network:          map[string]any{"test": true},
		OnTask: func(ctx context.Context, task Task) (string, error) {
			rec.record(task.Content)
			return "done: " + task.Content, nil
		},
	})
	go func() {
		_ = node.Run(runCtx)
	}()

	// Wait for the node to register and announce its profile
	var rootMsgID string
	deadline := time.After(5 * time.Second)
	for rootMsgID == "" {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for node to announce profile")
		default:
			time.Sleep(50 * time.Millisecond)
			rootMsgID = node.RootMessageID()
		}
	}

	controller, err := ioaclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.RegisterNode(ctx, "controller", nil); err != nil {
		t.Fatal(err)
	}
	space, err := controller.Space(ctx, "root-msg-test", "controller")
	if err != nil {
		t.Fatal(err)
	}

	// Send task by ref'ing the root message (no refs.nodes)
	_, err = controller.Send(ctx, space.ID, ioa.SendMessage{
		Content: map[string]any{
			"content": "task via root message ref",
		},
		Refs: &ioa.Ref{Messages: []string{rootMsgID}},
	})
	if err != nil {
		t.Fatal(err)
	}

	deadline = time.After(5 * time.Second)
	for {
		all, err := controller.Read(ctx, space.ID, ioa.ReadOptions{All: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, msg := range all {
			c, _ := msg.Content["content"].(string)
			if strings.Contains(c, "done: task via root message ref") {
				got := rec.tasks()
				if len(got) != 1 || got[0] != "task via root message ref" {
					t.Fatalf("handler tasks = %#v", got)
				}
				return
			}
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for root-message-ref task completion; messages=%d", len(all))
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func TestNodeRejectsTaskForOtherRootMessage(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore())
	server := httptest.NewServer(ioaserver.NewHandler(service))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	worker, err := ioaclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	rec := &taskRecorder{name: "reject-worker"}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	node := NewNode(NodeConfig{
		Client:           worker,
		NodeName:         "reject-worker",
		SpaceName:        "reject-test",
		SpaceDescription: "worker",
		PollInterval:     100 * time.Millisecond,
		Network:          map[string]any{"test": true},
		OnTask: func(ctx context.Context, task Task) (string, error) {
			rec.record(task.Content)
			return "should not happen", nil
		},
	})
	go func() {
		_ = node.Run(runCtx)
	}()

	// Wait for node to be ready
	deadline := time.After(5 * time.Second)
	for node.RootMessageID() == "" {
		select {
		case <-deadline:
			t.Fatal("timed out waiting for node to announce profile")
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}

	controller, err := ioaclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	controllerNode, err := controller.RegisterNode(ctx, "controller", nil)
	if err != nil {
		t.Fatal(err)
	}
	space, err := controller.Space(ctx, "reject-test", "controller")
	if err != nil {
		t.Fatal(err)
	}

	// Create a decoy message directed at the controller (not a broadcast)
	decoy, err := controller.Send(ctx, space.ID, ioa.SendMessage{
		Content: map[string]any{"content": "decoy root"},
		Refs:    &ioa.Ref{Nodes: []string{controllerNode.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Send task ref'ing the decoy message (not this node's root message)
	_, err = controller.Send(ctx, space.ID, ioa.SendMessage{
		Content: map[string]any{
			"content": "task for other node",
		},
		Refs: &ioa.Ref{Messages: []string{decoy.ID}},
	})
	if err != nil {
		t.Fatal(err)
	}

	// Wait a bit and verify the task was NOT processed
	time.Sleep(500 * time.Millisecond)
	if got := rec.tasks(); len(got) != 0 {
		t.Fatalf("node should not have processed task for other root message, but got: %#v", got)
	}
}

func TestSwarmFromIOAParsesNewAndLegacy(t *testing.T) {
	msg := ioa.Message{Content: map[string]any{"content": "scan this"}}
	sm, ok := swarmFromIOA(msg)
	if !ok || sm.Content != "scan this" {
		t.Fatalf("swarmFromIOA(new) = %#v, %v", sm, ok)
	}

	msg = ioa.Message{Content: map[string]any{
		"content": "full scan",
		"targets": []any{"10.0.0.0/24"},
		"meta":    map[string]any{"ip": "10.0.0.5"},
	}}
	sm, ok = swarmFromIOA(msg)
	if !ok || sm.Content != "full scan" || len(sm.Targets) != 1 || sm.Meta["ip"] != "10.0.0.5" {
		t.Fatalf("swarmFromIOA(new+targets+meta) = %#v, %v", sm, ok)
	}

	msg = ioa.Message{Content: map[string]any{"task": "legacy task"}}
	sm, ok = swarmFromIOA(msg)
	if !ok || sm.Content != "legacy task" {
		t.Fatalf("swarmFromIOA(legacy task) = %#v, %v", sm, ok)
	}

	msg = ioa.Message{Content: map[string]any{"prompt": "legacy prompt"}}
	sm, ok = swarmFromIOA(msg)
	if !ok || sm.Content != "legacy prompt" {
		t.Fatalf("swarmFromIOA(legacy prompt) = %#v, %v", sm, ok)
	}

	msg = ioa.Message{Content: map[string]any{"type": "note", "text": "hello"}}
	_, ok = swarmFromIOA(msg)
	if ok {
		t.Fatal("swarmFromIOA should reject messages without content/task/prompt")
	}
}

func TestSwarmContentRoundTrip(t *testing.T) {
	msg := SwarmMessage{
		Content: "scan these targets",
		Targets: []string{"10.0.0.0/24", "192.168.1.0/24"},
		Meta:    map[string]any{"ip": "10.0.0.5", "hostname": "scanner-1"},
	}
	raw := swarmContent(msg)
	parsed, ok := ParseSwarm(raw)
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

func TestIsTaskForNode(t *testing.T) {
	nodeID := "node-1"
	rootMsgID := "root-msg-1"

	// Broadcast: no refs
	msg := ioa.Message{Refs: ioa.Ref{}}
	if !isTaskForNode(msg, nodeID, rootMsgID) {
		t.Fatal("broadcast should be accepted")
	}

	// Node-directed: refs.nodes contains nodeID
	msg = ioa.Message{Refs: ioa.Ref{Nodes: []string{nodeID}}}
	if !isTaskForNode(msg, nodeID, rootMsgID) {
		t.Fatal("node-directed should be accepted")
	}

	// Node-directed: refs.nodes does NOT contain nodeID
	msg = ioa.Message{Refs: ioa.Ref{Nodes: []string{"other-node"}}}
	if isTaskForNode(msg, nodeID, rootMsgID) {
		t.Fatal("node-directed to other node should be rejected")
	}

	// Root-message-directed: refs.messages contains rootMsgID
	msg = ioa.Message{Refs: ioa.Ref{Messages: []string{rootMsgID}}}
	if !isTaskForNode(msg, nodeID, rootMsgID) {
		t.Fatal("root-message-directed should be accepted")
	}

	// Root-message-directed: refs.messages does NOT contain rootMsgID
	msg = ioa.Message{Refs: ioa.Ref{Messages: []string{"other-root"}}}
	if isTaskForNode(msg, nodeID, rootMsgID) {
		t.Fatal("root-message-directed to other root should be rejected")
	}

	// Empty rootMsgID: refs.messages should be rejected
	msg = ioa.Message{Refs: ioa.Ref{Messages: []string{"any-msg"}}}
	if isTaskForNode(msg, nodeID, "") {
		t.Fatal("should reject refs.messages when rootMsgID is empty")
	}
}

// ── Helpers ─────────────────────────────────────────────────────────

type taskRecorder struct {
	name string

	mu   sync.Mutex
	seen []string
}

func (r *taskRecorder) record(task string) {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = append(r.seen, task)
}

func (r *taskRecorder) tasks() []string {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]string(nil), r.seen...)
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
