package swarm

import (
	"context"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/ioa"
	ioaclient "github.com/chainreactors/ioa/client"
	ioaserver "github.com/chainreactors/ioa/server"
)

// TestE2ETwoSwarmNodesCoordinated simulates a real deployment:
//
//	IOA Server
//	  └── Space "pentest-case-001"
//	        ├── scanner-node  (skills: gogo, spray)
//	        ├── recon-node    (skills: neutron, cyberhub)
//	        └── controller    (human coordinator)
//
// The controller dispatches tasks through three routing modes:
//  1. refs.nodes     → directed to a specific node
//  2. refs.messages  → directed via root message ref (new!)
//  3. no refs        → broadcast to all nodes
func TestE2ETwoSwarmNodesCoordinated(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore())
	server := httptest.NewServer(ioaserver.NewHandler(service))
	defer server.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()

	spaceName := "pentest-case-001"

	// ── Start scanner node ──────────────────────────────────────
	scannerClient, err := ioaclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	scannerRec := &taskRecorder{name: "scanner"}
	scannerNode := NewNode(NodeConfig{
		Client:           scannerClient,
		NodeName:         "scanner-node",
		SpaceName:        spaceName,
		SpaceDescription: "port scanner and service detection",
		PollInterval:     100 * time.Millisecond,
		Prompt:           "scan targets for open ports and services",
		Skills:           []string{"gogo", "spray"},
		Network:          map[string]any{"hostname": "scanner-host", "cidr": "10.0.0.0/24"},
		OnTask: func(ctx context.Context, task Task) (string, error) {
			scannerRec.record(task.Content)
			if strings.Contains(task.Content, "port scan") {
				return "Found open ports: 22/ssh, 80/http, 443/https, 8080/http-proxy on 10.0.0.5", nil
			}
			if strings.Contains(task.Content, "hello") || strings.Contains(task.Content, "status") {
				return "scanner-node online, ready for tasks", nil
			}
			return fmt.Sprintf("scanner processed: %s", task.Content), nil
		},
	})

	// ── Start recon node ────────────────────────────────────────
	reconClient, err := ioaclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	reconRec := &taskRecorder{name: "recon"}
	reconNode := NewNode(NodeConfig{
		Client:           reconClient,
		NodeName:         "recon-node",
		SpaceName:        spaceName,
		SpaceDescription: "vulnerability recon and fingerprinting",
		PollInterval:     100 * time.Millisecond,
		Prompt:           "identify vulnerabilities and fingerprints",
		Skills:           []string{"neutron", "cyberhub"},
		Network:          map[string]any{"hostname": "recon-host", "cidr": "10.0.0.0/24"},
		OnTask: func(ctx context.Context, task Task) (string, error) {
			reconRec.record(task.Content)
			if strings.Contains(task.Content, "fingerprint") {
				return "Identified: nginx/1.24, OpenSSH 8.9, Apache Tomcat 9.0 on target", nil
			}
			if strings.Contains(task.Content, "hello") || strings.Contains(task.Content, "status") {
				return "recon-node online, ready for tasks", nil
			}
			return fmt.Sprintf("recon processed: %s", task.Content), nil
		},
	})

	// ── Launch both nodes ───────────────────────────────────────
	runCtx, stopNodes := context.WithCancel(ctx)
	defer stopNodes()
	go func() { _ = scannerNode.Run(runCtx) }()
	go func() { _ = reconNode.Run(runCtx) }()

	// Wait for both nodes to register and announce profiles
	waitFor(t, 5*time.Second, "scanner profile", func() bool {
		return scannerNode.RootMessageID() != ""
	})
	waitFor(t, 5*time.Second, "recon profile", func() bool {
		return reconNode.RootMessageID() != ""
	})

	t.Logf("scanner node: id=%s root_msg=%s", scannerNode.NodeID(), scannerNode.RootMessageID())
	t.Logf("recon node:   id=%s root_msg=%s", reconNode.NodeID(), reconNode.RootMessageID())

	// ── Setup controller ────────────────────────────────────────
	controller, err := ioaclient.NewClient(server.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := controller.RegisterNode(ctx, "coordinator", nil); err != nil {
		t.Fatal(err)
	}
	space, err := controller.Space(ctx, spaceName, "human coordinator")
	if err != nil {
		t.Fatal(err)
	}

	// ── Verify profile announcements ────────────────────────────
	t.Run("profiles_visible", func(t *testing.T) {
		all, err := controller.Read(ctx, space.ID, ioa.ReadOptions{All: true})
		if err != nil {
			t.Fatal(err)
		}
		profiles := filterByContent(all, "joined the swarm")
		if len(profiles) != 2 {
			t.Fatalf("expected 2 profile announcements, got %d", len(profiles))
		}
		for _, p := range profiles {
			meta, _ := p.Content["meta"].(map[string]any)
			t.Logf("  profile: sender=%s hostname=%v capabilities=%v",
				p.Sender, meta["hostname"], meta["capabilities"])
		}
	})

	// ── Test 1: Directed task via refs.nodes ─────────────────────
	t.Run("directed_by_node_id", func(t *testing.T) {
		scannerRec.reset()
		reconRec.reset()

		_, err := controller.Send(ctx, space.ID, ioa.SendMessage{
			Content: map[string]any{
				"content": "Run port scan on 10.0.0.5",
				"targets": []string{"10.0.0.5"},
			},
			Refs: &ioa.Ref{Nodes: []string{scannerNode.NodeID()}},
		})
		if err != nil {
			t.Fatal(err)
		}

		waitFor(t, 5*time.Second, "scanner result", func() bool {
			return len(scannerRec.tasks()) > 0
		})

		got := scannerRec.tasks()
		if len(got) != 1 || !strings.Contains(got[0], "port scan") {
			t.Fatalf("scanner tasks = %#v", got)
		}
		if len(reconRec.tasks()) != 0 {
			t.Fatalf("recon should not have received directed task, got: %#v", reconRec.tasks())
		}

		// Verify result was posted to space
		all, _ := controller.Read(ctx, space.ID, ioa.ReadOptions{All: true})
		found := false
		for _, msg := range all {
			c, _ := msg.Content["content"].(string)
			if strings.Contains(c, "22/ssh") && strings.Contains(c, "80/http") {
				found = true
				t.Logf("  scanner result: %s", c)
			}
		}
		if !found {
			t.Fatal("scanner result not found in space")
		}
	})

	// ── Test 2: Directed task via refs.messages (root message) ───
	t.Run("directed_by_root_message_ref", func(t *testing.T) {
		reconRec.reset()
		scannerRec.reset()

		_, err := controller.Send(ctx, space.ID, ioa.SendMessage{
			Content: map[string]any{
				"content": "Run fingerprint detection on 10.0.0.5",
				"targets": []string{"10.0.0.5"},
			},
			Refs: &ioa.Ref{Messages: []string{reconNode.RootMessageID()}},
		})
		if err != nil {
			t.Fatal(err)
		}

		waitFor(t, 5*time.Second, "recon result via root ref", func() bool {
			return len(reconRec.tasks()) > 0
		})

		got := reconRec.tasks()
		if len(got) != 1 || !strings.Contains(got[0], "fingerprint") {
			t.Fatalf("recon tasks = %#v", got)
		}
		if len(scannerRec.tasks()) != 0 {
			t.Fatalf("scanner should not have received task directed at recon's root message, got: %#v", scannerRec.tasks())
		}

		all, _ := controller.Read(ctx, space.ID, ioa.ReadOptions{All: true})
		found := false
		for _, msg := range all {
			c, _ := msg.Content["content"].(string)
			if strings.Contains(c, "nginx/1.24") {
				found = true
				t.Logf("  recon result: %s", c)
			}
		}
		if !found {
			t.Fatal("recon result not found in space")
		}
	})

	// ── Test 3: Broadcast task (no refs) ─────────────────────────
	t.Run("broadcast_to_all", func(t *testing.T) {
		scannerRec.reset()
		reconRec.reset()

		_, err := controller.Send(ctx, space.ID, ioa.SendMessage{
			Content: map[string]any{
				"content": "Report your current status",
			},
		})
		if err != nil {
			t.Fatal(err)
		}

		waitFor(t, 5*time.Second, "both nodes respond to broadcast", func() bool {
			return len(scannerRec.tasks()) > 0 && len(reconRec.tasks()) > 0
		})

		scannerTasks := scannerRec.tasks()
		reconTasks := reconRec.tasks()
		if len(scannerTasks) != 1 || !strings.Contains(scannerTasks[0], "status") {
			t.Fatalf("scanner broadcast tasks = %#v", scannerTasks)
		}
		if len(reconTasks) != 1 || !strings.Contains(reconTasks[0], "status") {
			t.Fatalf("recon broadcast tasks = %#v", reconTasks)
		}

		all, _ := controller.Read(ctx, space.ID, ioa.ReadOptions{All: true})
		onlineCount := 0
		for _, msg := range all {
			c, _ := msg.Content["content"].(string)
			if strings.Contains(c, "online, ready for tasks") {
				onlineCount++
				t.Logf("  broadcast reply: %s", c)
			}
		}
		if onlineCount < 2 {
			t.Fatalf("expected 2 online replies, got %d", onlineCount)
		}
	})

	// ── Test 4: Verify message threading (refs.messages on replies) ──
	t.Run("reply_threading", func(t *testing.T) {
		scannerRec.reset()

		task, err := controller.Send(ctx, space.ID, ioa.SendMessage{
			Content: map[string]any{
				"content": "Run port scan on 192.168.1.1",
				"targets": []string{"192.168.1.1"},
			},
			Refs: &ioa.Ref{Nodes: []string{scannerNode.NodeID()}},
		})
		if err != nil {
			t.Fatal(err)
		}

		waitFor(t, 5*time.Second, "scanner reply", func() bool {
			return len(scannerRec.tasks()) > 0
		})

		// Read the conversation thread under this task
		thread, err := controller.Read(ctx, space.ID, ioa.ReadOptions{MessageID: task.ID})
		if err != nil {
			t.Fatal(err)
		}

		// Should have: the task itself + "Accepted task" + result
		if len(thread) < 3 {
			t.Fatalf("expected >= 3 messages in thread, got %d", len(thread))
		}

		hasAccept := false
		hasResult := false
		for _, msg := range thread {
			c, _ := msg.Content["content"].(string)
			if strings.Contains(c, "Accepted task") {
				hasAccept = true
				if !containsRef(msg.Refs.Messages, task.ID) {
					t.Fatal("accept message should ref the task")
				}
			}
			if strings.Contains(c, "22/ssh") {
				hasResult = true
				if !containsRef(msg.Refs.Messages, task.ID) {
					t.Fatal("result message should ref the task")
				}
			}
		}
		if !hasAccept {
			t.Fatal("missing 'Accepted task' in thread")
		}
		if !hasResult {
			t.Fatal("missing result in thread")
		}
		t.Logf("  thread has %d messages (task + accept + result)", len(thread))
	})

	// ── Test 5: Cross-verify IOA read modes ─────────────────────
	t.Run("ioa_read_modes", func(t *testing.T) {
		// All messages
		all, err := controller.Read(ctx, space.ID, ioa.ReadOptions{All: true})
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("  total messages in space: %d", len(all))
		if len(all) < 10 {
			t.Fatalf("expected >= 10 messages total, got %d", len(all))
		}

		// Space info
		info, err := controller.GetSpaceInfo(ctx, space.ID)
		if err != nil {
			t.Fatal(err)
		}
		t.Logf("  space: name=%q nodes=%d messages=%d", info.Name, len(info.Nodes), info.MessageCount)
		if len(info.Nodes) < 3 {
			t.Fatalf("expected >= 3 nodes (2 workers + controller), got %d", len(info.Nodes))
		}
	})
}

func (r *taskRecorder) reset() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.seen = nil
}

func waitFor(t *testing.T, timeout time.Duration, desc string, cond func() bool) {
	t.Helper()
	deadline := time.After(timeout)
	for {
		if cond() {
			return
		}
		select {
		case <-deadline:
			t.Fatalf("timed out waiting for: %s", desc)
		default:
			time.Sleep(50 * time.Millisecond)
		}
	}
}

func filterByContent(messages []ioa.Message, substr string) []ioa.Message {
	var result []ioa.Message
	for _, msg := range messages {
		c, _ := msg.Content["content"].(string)
		if strings.Contains(c, substr) {
			result = append(result, msg)
		}
	}
	return result
}
