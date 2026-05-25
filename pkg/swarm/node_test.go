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

func TestNodeDynamicCrons(t *testing.T) {
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
	space, err := controller.Space(ctx, "cron-test", "controller")
	if err != nil {
		t.Fatal(err)
	}
	controller.Send(ctx, space.ID, ioa.SendMessage{
		Content: map[string]any{"content": "initial context"},
	})

	worker, err := ioaclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	rec := &taskRecorder{name: "cron-worker"}
	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	node := NewNode(NodeConfig{
		Client:                worker,
		NodeName:              "cron-worker",
		SpaceName:             "cron-test",
		SpaceDescription:      "worker",
		PollInterval:          100 * time.Millisecond,
		HeartbeatContextLimit: 50,
		Network:               map[string]any{"test": true},
		OnTask: func(ctx context.Context, task Task) (string, error) {
			return "ok", nil
		},
		OnHeartbeat: func(ctx context.Context, prompt string) (string, error) {
			rec.record(prompt)
			if strings.Contains(prompt, "fast-check") {
				return "fast done", nil
			}
			return "slow done", nil
		},
	})
	go func() {
		_ = node.Run(runCtx)
	}()

	// Wait for node to be ready then add crons dynamically
	waitFor(t, 3*time.Second, "node ready", func() bool {
		return node.RootMessageID() != ""
	})
	if err := node.AddCron(CronTask{Name: "fast-check", Interval: 50 * time.Millisecond, Prompt: "fast check task"}); err != nil {
		t.Fatal(err)
	}
	if err := node.AddCron(CronTask{Name: "slow-report", Interval: 80 * time.Millisecond, Prompt: "slow report task"}); err != nil {
		t.Fatal(err)
	}
	if crons := node.ListCrons(); len(crons) != 2 {
		t.Fatalf("expected 2 crons, got %d", len(crons))
	}

	deadline := time.After(5 * time.Second)
	for {
		prompts := rec.tasks()
		hasFast := false
		hasSlow := false
		for _, p := range prompts {
			if strings.Contains(p, "fast check task") && strings.Contains(p, "fast-check") {
				hasFast = true
			}
			if strings.Contains(p, "slow report task") && strings.Contains(p, "slow-report") {
				hasSlow = true
			}
		}
		if hasFast && hasSlow {
			// Verify both cron names appear in space messages
			all, _ := controller.Read(ctx, space.ID, ioa.ReadOptions{All: true})
			fastCount := 0
			slowCount := 0
			for _, msg := range all {
				c, _ := msg.Content["content"].(string)
				if c == "fast done" {
					fastCount++
				}
				if c == "slow done" {
					slowCount++
				}
			}
			if fastCount == 0 || slowCount == 0 {
				t.Fatalf("expected both cron results in space; fast=%d slow=%d", fastCount, slowCount)
			}
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for both crons; prompts=%d", len(prompts))
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

func TestIsTaskMessage(t *testing.T) {
	nodeID := "node-1"
	rootMsgID := "root-msg-1"
	empty := SwarmMessage{}

	// Idle broadcast: no refs, no meta kind -> legacy task.
	msg := ioa.Message{Refs: ioa.Ref{}}
	if !isTaskMessage(msg, empty, nodeID, rootMsgID, false) {
		t.Fatal("broadcast should be accepted")
	}
	if isTaskMessage(msg, empty, nodeID, rootMsgID, true) {
		t.Fatal("active broadcast without task_dispatch should be peer chatter")
	}

	// Node-directed: refs.nodes contains nodeID.
	msg = ioa.Message{Refs: ioa.Ref{Nodes: []string{nodeID}}}
	if !isTaskMessage(msg, empty, nodeID, rootMsgID, false) {
		t.Fatal("node-directed should be accepted")
	}
	if isTaskMessage(msg, empty, nodeID, rootMsgID, true) {
		t.Fatal("active node-directed without task_dispatch should be peer chatter")
	}

	// Node-directed: refs.nodes does NOT contain nodeID.
	msg = ioa.Message{Refs: ioa.Ref{Nodes: []string{"other-node"}}}
	if isTaskMessage(msg, empty, nodeID, rootMsgID, false) {
		t.Fatal("node-directed to other node should be rejected")
	}

	// Root-message-directed: refs.messages contains rootMsgID.
	msg = ioa.Message{Refs: ioa.Ref{Messages: []string{rootMsgID}}}
	if !isTaskMessage(msg, empty, nodeID, rootMsgID, false) {
		t.Fatal("root-message-directed should be accepted")
	}
	if isTaskMessage(msg, empty, nodeID, rootMsgID, true) {
		t.Fatal("active root-message-directed without task_dispatch should be peer chatter")
	}

	// Root-message-directed: refs.messages does NOT contain rootMsgID.
	msg = ioa.Message{Refs: ioa.Ref{Messages: []string{"other-root"}}}
	if isTaskMessage(msg, empty, nodeID, rootMsgID, false) {
		t.Fatal("root-message-directed to other root should be rejected")
	}

	// Empty rootMsgID: refs.messages should be rejected.
	msg = ioa.Message{Refs: ioa.Ref{Messages: []string{"any-msg"}}}
	if isTaskMessage(msg, empty, nodeID, "", false) {
		t.Fatal("should reject refs.messages when rootMsgID is empty")
	}

	// Broadcast with explicit task_dispatch meta kind → task.
	msg = ioa.Message{Refs: ioa.Ref{}}
	sm := SwarmMessage{Meta: map[string]any{"kind": "task_dispatch"}}
	if !isTaskMessage(msg, sm, nodeID, rootMsgID, true) {
		t.Fatal("task_dispatch broadcast should be accepted")
	}

	// Broadcast with peer chatter kind → NOT a task.
	sm = SwarmMessage{Meta: map[string]any{"kind": "update"}}
	if isTaskMessage(msg, sm, nodeID, rootMsgID, false) {
		t.Fatal("peer chatter (kind=update) should NOT be treated as task")
	}
}

// TestPeerMessagesForwardedToActiveTask verifies the new auto-injection
// mechanism: while a worker is running OnTask, partner messages arriving in
// the same space (without task_dispatch meta) are forwarded into the task's
// Peers channel rather than triggering a second task.
func TestPeerMessagesForwardedToActiveTask(t *testing.T) {
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
	space, err := controller.Space(ctx, "peer-routing", "peer routing case")
	if err != nil {
		t.Fatal(err)
	}

	peer, err := ioaclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	peerNode, err := peer.RegisterNode(ctx, "partner", nil)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peer.Space(ctx, "peer-routing", "peer"); err != nil {
		t.Fatal(err)
	}

	worker, err := ioaclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}

	taskStarted := make(chan struct{})
	releaseTask := make(chan struct{})
	receivedPeers := make(chan PeerMessage, 4)

	runCtx, stopWorker := context.WithCancel(ctx)
	defer stopWorker()
	node := NewNode(NodeConfig{
		Client:           worker,
		NodeName:         "worker-peer",
		SpaceName:        "peer-routing",
		SpaceDescription: "worker",
		PollInterval:     100 * time.Millisecond,
		OnTask: func(ctx context.Context, task Task) (string, error) {
			close(taskStarted)
			select {
			case <-releaseTask:
				return "drained", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
		OnPeer: func(peer PeerMessage) bool {
			select {
			case receivedPeers <- peer:
				return true
			default:
				return false
			}
		},
	})
	go func() { _ = node.Run(runCtx) }()

	// Dispatch a task explicitly (refs.nodes), then wait for OnTask to start.
	// We need to know the worker's node ID; the announce profile carries it.
	deadline := time.After(5 * time.Second)
	var workerID string
	for workerID == "" {
		select {
		case <-deadline:
			t.Fatal("worker did not announce in time")
		default:
		}
		all, err := controller.Read(ctx, space.ID, ioa.ReadOptions{All: true})
		if err != nil {
			t.Fatal(err)
		}
		for _, m := range all {
			c, _ := m.Content["content"].(string)
			if strings.Contains(c, "joined the swarm") && m.Sender != peerNode.ID {
				workerID = m.Sender
				break
			}
		}
		time.Sleep(20 * time.Millisecond)
	}

	if _, err := controller.Send(ctx, space.ID, ioa.SendMessage{
		Content: map[string]any{"content": "do work", "meta": map[string]any{"kind": "task_dispatch"}},
		Refs:    &ioa.Ref{Nodes: []string{workerID}},
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case <-taskStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("task did not start within 5s")
	}

	// Now send a peer chatter message (kind=update) — should NOT start a second task,
	// should land in task.Peers.
	if _, err := peer.Send(ctx, space.ID, ioa.SendMessage{
		Content: map[string]any{"content": "watch example.com", "meta": map[string]any{"kind": "update"}},
	}); err != nil {
		t.Fatal(err)
	}

	select {
	case p := <-receivedPeers:
		if p.Content != "watch example.com" {
			t.Fatalf("peer content = %q, want watch example.com", p.Content)
		}
		if p.Sender != peerNode.ID {
			t.Fatalf("peer sender = %q, want %q", p.Sender, peerNode.ID)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("peer message not forwarded to active task")
	}

	// Directed partner messages are also peer chatter while a task is active
	// unless they explicitly declare task_dispatch.
	if _, err := peer.Send(ctx, space.ID, ioa.SendMessage{
		Content: map[string]any{"content": "direct note"},
		Refs:    &ioa.Ref{Nodes: []string{workerID}},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-receivedPeers:
		if p.Content != "direct note" {
			t.Fatalf("direct peer content = %q, want direct note", p.Content)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("direct peer message not forwarded to active task")
	}

	// Messages explicitly addressed to another node should not be injected here.
	if _, err := peer.Send(ctx, space.ID, ioa.SendMessage{
		Content: map[string]any{"content": "not for this worker"},
		Refs:    &ioa.Ref{Nodes: []string{controllerNode.ID}},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-receivedPeers:
		t.Fatalf("unexpected peer message for another node: %#v", p)
	case <-time.After(300 * time.Millisecond):
	}

	// Structured IOA messages from the ioa skill do not have content["content"].
	// They should still be delivered via inbox with the raw content JSON-formatted.
	if _, err := peer.Send(ctx, space.ID, ioa.SendMessage{
		Content: map[string]any{"kind": "asset", "domains": []string{"example.com"}},
	}); err != nil {
		t.Fatal(err)
	}
	select {
	case p := <-receivedPeers:
		if p.Content != "" {
			t.Fatalf("structured peer content = %q, want empty", p.Content)
		}
		if got, _ := p.RawContent["kind"].(string); got != "asset" {
			t.Fatalf("structured peer kind = %q, want asset", got)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("structured peer message not forwarded to active task")
	}

	close(releaseTask)
}

// TestTasksArrivingWhileBusyAreQueued verifies that task_dispatch messages
// arriving while another task is in flight are queued (not dropped) and run
// to completion in arrival order after the current task finishes.
//
// Regression: previously these messages were marked processed before the
// busy-check, so they were stamped as "handled" and never retried by catchUp.
func TestTasksArrivingWhileBusyAreQueued(t *testing.T) {
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
	space, err := controller.Space(ctx, "queue-case", "controller")
	if err != nil {
		t.Fatal(err)
	}

	worker, err := ioaclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	workerNode, err := worker.RegisterNode(ctx, "queue-worker", nil)
	if err != nil {
		t.Fatal(err)
	}

	rec := &taskRecorder{name: "queue-worker"}
	// Block the first task until released so we can dispatch follow-ups while
	// it's running.
	release := make(chan struct{})
	firstStarted := make(chan struct{})
	var startedOnce sync.Once

	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	node := NewNode(NodeConfig{
		Client:           worker,
		NodeName:         "queue-worker",
		SpaceName:        "queue-case",
		SpaceDescription: "worker",
		PollInterval:     100 * time.Millisecond,
		Network:          map[string]any{"test": true},
		OnTask: func(ctx context.Context, task Task) (string, error) {
			startedOnce.Do(func() { close(firstStarted) })
			rec.record(task.Content)
			if task.Content == "task-1" {
				select {
				case <-release:
				case <-ctx.Done():
					return "", ctx.Err()
				}
			}
			return "done: " + task.Content, nil
		},
	})
	go func() { _ = node.Run(runCtx) }()

	waitFor(t, 5*time.Second, "worker announce", func() bool {
		return node.RootMessageID() != ""
	})

	dispatch := func(content string) {
		t.Helper()
		_, err := controller.Send(ctx, space.ID, ioa.SendMessage{
			Content: map[string]any{
				"content": content,
				"meta":    map[string]any{"kind": "task_dispatch"},
			},
			Refs: &ioa.Ref{Nodes: []string{workerNode.ID}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	dispatch("task-1")
	select {
	case <-firstStarted:
	case <-time.After(5 * time.Second):
		t.Fatal("first task never started")
	}

	// While task-1 is blocked inside OnTask, queue two more.
	dispatch("task-2")
	dispatch("task-3")

	// Give the routing loop a moment to see and queue them.
	time.Sleep(300 * time.Millisecond)

	if got := rec.tasks(); len(got) != 1 || got[0] != "task-1" {
		t.Fatalf("before release: rec.tasks()=%#v, want [task-1]", got)
	}

	close(release)

	waitFor(t, 5*time.Second, "all 3 tasks ran", func() bool {
		return len(rec.tasks()) >= 3
	})

	got := rec.tasks()
	if len(got) != 3 || got[0] != "task-1" || got[1] != "task-2" || got[2] != "task-3" {
		t.Fatalf("rec.tasks()=%#v, want [task-1 task-2 task-3]", got)
	}

	// Verify all three completion reports made it back to the space.
	all, err := controller.Read(ctx, space.ID, ioa.ReadOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}
	results := map[string]bool{}
	for _, msg := range all {
		c, _ := msg.Content["content"].(string)
		if strings.HasPrefix(c, "done: task-") {
			results[c] = true
		}
	}
	for _, want := range []string{"done: task-1", "done: task-2", "done: task-3"} {
		if !results[want] {
			t.Fatalf("missing completion %q in space; got %#v", want, results)
		}
	}
}

// TestPeerOverflowRecoversViaCatchUp verifies that peer messages arriving
// faster than the consumer can drain do not get permanently lost: when the
// per-task peer buffer is full we defer (don't mark processed) so the next
// catchUp tick re-delivers as soon as the consumer drains some.
func TestPeerOverflowRecoversViaCatchUp(t *testing.T) {
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
	space, err := controller.Space(ctx, "overflow-case", "controller")
	if err != nil {
		t.Fatal(err)
	}

	peerClient, err := ioaclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := peerClient.RegisterNode(ctx, "peer-sender", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := peerClient.Space(ctx, "overflow-case", "peer"); err != nil {
		t.Fatal(err)
	}

	worker, err := ioaclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	workerNode, err := worker.RegisterNode(ctx, "overflow-worker", nil)
	if err != nil {
		t.Fatal(err)
	}

	// Send 2× the peer buffer; the consumer drains slowly so most arrivals
	// will hit the "buffer full → defer" path. They should still all show up.
	const totalPeers = 2 * defaultPeerBufferSize
	gotPeers := make(chan PeerMessage, totalPeers)
	drainPause := 5 * time.Millisecond
	startDraining := make(chan struct{})
	taskDone := make(chan struct{})

	runCtx, stop := context.WithCancel(ctx)
	defer stop()
	node := NewNode(NodeConfig{
		Client:           worker,
		NodeName:         "overflow-worker",
		SpaceName:        "overflow-case",
		SpaceDescription: "worker",
		PollInterval:     50 * time.Millisecond,
		Network:          map[string]any{"test": true},
		OnTask: func(ctx context.Context, task Task) (string, error) {
			select {
			case <-startDraining:
			case <-ctx.Done():
				return "", ctx.Err()
			}
			select {
			case <-taskDone:
				return "drained", nil
			case <-ctx.Done():
				return "", ctx.Err()
			}
		},
		OnPeer: func(peer PeerMessage) bool {
			select {
			case gotPeers <- peer:
				if len(gotPeers) >= totalPeers {
					select {
					case <-taskDone:
					default:
						close(taskDone)
					}
				}
				time.Sleep(drainPause)
				return true
			default:
				return false
			}
		},
	})
	go func() { _ = node.Run(runCtx) }()

	waitFor(t, 5*time.Second, "worker announce", func() bool {
		return node.RootMessageID() != ""
	})

	// Kick off a task so peer routing has an active consumer.
	if _, err := controller.Send(ctx, space.ID, ioa.SendMessage{
		Content: map[string]any{
			"content": "drain peers",
			"meta":    map[string]any{"kind": "task_dispatch"},
		},
		Refs: &ioa.Ref{Nodes: []string{workerNode.ID}},
	}); err != nil {
		t.Fatal(err)
	}

	// Fire all peer messages while the consumer is still gated on
	// startDraining. The node's buffer is 64, so the latter ~totalPeers-64
	// will all hit the defer path.
	for i := 0; i < totalPeers; i++ {
		if _, err := peerClient.Send(ctx, space.ID, ioa.SendMessage{
			Content: map[string]any{
				"content": fmt.Sprintf("peer-%d", i),
				"meta":    map[string]any{"kind": "update"},
			},
		}); err != nil {
			t.Fatal(err)
		}
	}

	// Give the routing loop time to attempt all deliveries.
	time.Sleep(300 * time.Millisecond)
	close(startDraining)

	select {
	case <-taskDone:
	case <-time.After(15 * time.Second):
		t.Fatalf("only %d/%d peer messages delivered", len(gotPeers), totalPeers)
	}

	if len(gotPeers) < totalPeers {
		t.Fatalf("delivered %d peers, want %d", len(gotPeers), totalPeers)
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
