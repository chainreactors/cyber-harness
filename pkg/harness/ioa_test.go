//go:build e2e

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/ioa"
	ioaclient "github.com/chainreactors/ioa/client"
	ioaserver "github.com/chainreactors/ioa/server"
)

func TestIOALoopReceivesTask(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore())
	srv := httptest.NewServer(ioaserver.NewHandler(service))
	defer srv.Close()

	h := New(t)

	go func() {
		h.RunWithTimeout(60*time.Second,
			"agent", "--loop",
			"--ioa-url", srv.URL,
			"--space", "test-loop",
			"-p", "I am a test worker",
			"--timeout", "45",
		)
	}()

	time.Sleep(3 * time.Second)

	controller, err := ioaclient.NewClient(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := controller.RegisterNode(ctx, "controller", nil); err != nil {
		t.Fatal(err)
	}
	space, err := controller.Space(ctx, "test-loop", "e2e test")
	if err != nil {
		t.Fatal(err)
	}

	nodes, err := controller.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("no worker nodes registered in space")
	}
	workerNodeID := nodes[0].ID

	_, err = controller.Send(ctx, space.ID, ioa.SendMessage{
		Content: map[string]any{"content": "Run 'echo ioa_task_received' in bash and report the output."},
		Refs:    &ioa.Ref{Nodes: []string{workerNodeID}},
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(30 * time.Second)

	requireIOAMessageContains(t, controller, ctx, space.ID, "ioa_task_received")
}

func TestIOALoopMultipleWorkers(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore())
	srv := httptest.NewServer(ioaserver.NewHandler(service))
	defer srv.Close()

	h := New(t)

	for i := 1; i <= 2; i++ {
		i := i
		go func() {
			h.RunWithTimeout(45*time.Second,
				"agent", "--loop",
				"--ioa-url", srv.URL,
				"--space", "multi-worker",
				"--ioa-node-name", fmt.Sprintf("worker-%d", i),
				"-p", fmt.Sprintf("I am worker %d", i),
				"--timeout", "40",
			)
		}()
	}

	time.Sleep(4 * time.Second)

	controller, err := ioaclient.NewClient(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := controller.RegisterNode(ctx, "controller", nil); err != nil {
		t.Fatal(err)
	}
	if _, err := controller.Space(ctx, "multi-worker", "e2e multi"); err != nil {
		t.Fatal(err)
	}

	nodes, err := controller.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	workerCount := 0
	for _, n := range nodes {
		if strings.HasPrefix(n.Name, "worker-") {
			workerCount++
		}
	}
	if workerCount < 2 {
		t.Fatalf("expected ≥2 worker nodes, got %d (total nodes: %d)", workerCount, len(nodes))
	}
}

func TestIOALoopPeerMessage(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore())
	srv := httptest.NewServer(ioaserver.NewHandler(service))
	defer srv.Close()

	h := New(t)

	go func() {
		h.RunWithTimeout(45*time.Second,
			"agent", "--loop",
			"--ioa-url", srv.URL,
			"--space", "peer-test",
			"-p", "test worker",
			"--timeout", "40",
		)
	}()

	time.Sleep(3 * time.Second)

	controller, err := ioaclient.NewClient(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := controller.RegisterNode(ctx, "controller", nil); err != nil {
		t.Fatal(err)
	}
	space, err := controller.Space(ctx, "peer-test", "e2e peer")
	if err != nil {
		t.Fatal(err)
	}

	nodes, err := controller.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if len(nodes) == 0 {
		t.Fatal("no worker nodes")
	}
	workerNodeID := nodes[0].ID

	_, err = controller.Send(ctx, space.ID, ioa.SendMessage{
		Content: map[string]any{"content": "Run echo peer_hello and report result"},
		Refs:    &ioa.Ref{Nodes: []string{workerNodeID}},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = controller.Send(ctx, space.ID, ioa.SendMessage{
		Content: map[string]any{"content": "Additional context: also run 'echo peer_context_received'"},
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(25 * time.Second)

	requireIOAMessageContains(t, controller, ctx, space.ID, "peer_hello")
}

func TestIOATaskSpawnsSubagents(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore())
	srv := httptest.NewServer(ioaserver.NewHandler(service))
	defer srv.Close()

	h := New(t)

	go func() {
		h.RunWithTimeout(90*time.Second,
			"agent", "--loop",
			"--ioa-url", srv.URL,
			"--space", "subagent-fan",
			"-p", "I am a worker that parallelizes tasks using subagents",
			"--timeout", "80",
		)
	}()

	time.Sleep(4 * time.Second)

	controller, err := ioaclient.NewClient(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := controller.RegisterNode(ctx, "controller", nil); err != nil {
		t.Fatal(err)
	}
	space, err := controller.Space(ctx, "subagent-fan", "e2e")
	if err != nil {
		t.Fatal(err)
	}

	nodes, err := controller.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var workerNodeID string
	for _, n := range nodes {
		if n.Name != "controller" {
			workerNodeID = n.ID
			break
		}
	}
	if workerNodeID == "" {
		t.Fatal("no worker node found")
	}

	_, err = controller.Send(ctx, space.ID, ioa.SendMessage{
		Content: map[string]any{
			"content": "I need you to gather system info in parallel. " +
				"Create 2 async subagents: one runs 'echo subagent_alpha_ok' in bash, " +
				"the other runs 'echo subagent_beta_ok' in bash. " +
				"Wait for both results, then respond with a combined summary that includes both markers.",
		},
		Refs: &ioa.Ref{Nodes: []string{workerNodeID}},
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(60 * time.Second)

	requireIOAMessageContains(t, controller, ctx, space.ID, "subagent_alpha_ok")
	requireIOAMessageContains(t, controller, ctx, space.ID, "subagent_beta_ok")
}

func TestIOATwoWorkersDispatch(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore())
	srv := httptest.NewServer(ioaserver.NewHandler(service))
	defer srv.Close()

	h := New(t)

	for i := 1; i <= 2; i++ {
		i := i
		go func() {
			h.RunWithTimeout(75*time.Second,
				"agent", "--loop",
				"--ioa-url", srv.URL,
				"--space", "dispatch-2",
				"--ioa-node-name", fmt.Sprintf("worker-%d", i),
				"-p", fmt.Sprintf("I am worker %d", i),
				"--timeout", "70",
			)
		}()
	}

	time.Sleep(5 * time.Second)

	controller, err := ioaclient.NewClient(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := controller.RegisterNode(ctx, "controller", nil); err != nil {
		t.Fatal(err)
	}
	space, err := controller.Space(ctx, "dispatch-2", "e2e dispatch")
	if err != nil {
		t.Fatal(err)
	}

	nodes, err := controller.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var workers []ioa.Node
	for _, n := range nodes {
		if strings.HasPrefix(n.Name, "worker-") {
			workers = append(workers, n)
		}
	}
	if len(workers) < 2 {
		t.Fatalf("expected ≥2 workers, got %d", len(workers))
	}

	for i, w := range workers {
		marker := fmt.Sprintf("dispatch_marker_%d", i+1)
		_, err = controller.Send(ctx, space.ID, ioa.SendMessage{
			Content: map[string]any{
				"content": fmt.Sprintf("Run 'echo %s' in bash and report.", marker),
			},
			Refs: &ioa.Ref{Nodes: []string{w.ID}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	time.Sleep(45 * time.Second)

	requireIOAMessageContains(t, controller, ctx, space.ID, "dispatch_marker_1")
	requireIOAMessageContains(t, controller, ctx, space.ID, "dispatch_marker_2")
}

// requireIOAMessageContains checks that at least one message in the space contains substr.
func requireIOAMessageContains(t *testing.T, client *ioaclient.Client, ctx context.Context, spaceID, substr string) {
	t.Helper()
	msgs, err := client.Read(ctx, spaceID, ioa.ReadOptions{All: true})
	if err != nil {
		t.Fatalf("read space: %v", err)
	}
	for _, m := range msgs {
		raw, _ := json.Marshal(m.Content)
		if strings.Contains(string(raw), substr) {
			return
		}
	}
	var summaries []string
	for _, m := range msgs {
		raw, _ := json.Marshal(m.Content)
		summaries = append(summaries, clip(string(raw), 200))
	}
	t.Fatalf("no IOA message contains %q:\n%s", substr, strings.Join(summaries, "\n"))
}
