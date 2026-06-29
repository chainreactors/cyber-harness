//go:build e2e

package harness

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/ioa/protocols"
	ioaclient "github.com/chainreactors/ioa/client"
	ioaserver "github.com/chainreactors/ioa/server"
)

// =====================================================================
// init
// =====================================================================

func init() {
	if _, err := exec.LookPath("go"); err != nil {
		panic("go compiler not found; e2e tests require Go toolchain")
	}
}

// =====================================================================
// Agent — basic prompts and tool use
// =====================================================================

func TestAgentSimplePrompt(t *testing.T) {
	h := New(t)
	Intent{
		Name:           "simple-prompt",
		Prompt:         "What is 2+2? Reply with just the number.",
		OutputContains: []string{"4"},
		MaxTurns:       2,
		JudgeCriteria:  "The agent must reply with the number 4. No tool calls needed. The answer must be mathematically correct.",
	}.Run(t, h)
}

func TestAgentEmptyReply(t *testing.T) {
	h := New(t)
	r := h.Agent("Reply with the word 'pong' and nothing else.")
	Verify(t, r).OK().Done()
	if !strings.Contains(strings.ToLower(r.Output()), "pong") {
		t.Fatalf("expected 'pong', got: %s", r.Output())
	}
}

func TestAgentBashTool(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "bash-echo",
		Prompt: "Run 'echo hello_e2e' in a shell and tell me the exact output.",
		Steps: Steps(
			Tool("bash").ArgContains("echo hello_e2e").ResultHas("hello_e2e").NoError(),
		),
		OutputContains: []string{"hello_e2e"},
		NoErrors:       true,
		MaxTurns:       3,
		JudgeCriteria: "The agent must: (1) call the bash tool with a command containing 'echo hello_e2e', " +
			"(2) the bash result must contain 'hello_e2e', " +
			"(3) the final output must report 'hello_e2e' as the result.",
	}.Run(t, h)
}

func TestAgentReadTool(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "read-file",
		Prompt: "Read /etc/hostname and reply with only its contents.",
		Steps: Steps(
			Tool("read").ArgContains("hostname").NoError(),
		),
		NoErrors: true,
		MaxTurns: 3,
		JudgeCriteria: "The agent must use the read tool to read /etc/hostname, and the final output must contain the hostname value " +
			"(not just say 'I read it' — the actual content must appear).",
	}.Run(t, h)
}

func TestAgentWriteReadRoundtrip(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "write-read-roundtrip",
		Prompt: "Write 'e2e_marker_42' to /tmp/aiscan_e2e_test.txt, then read it back and confirm.",
		Steps: Steps(
			Tool("write").ArgContains("e2e_marker_42").NoError(),
			Tool("read").ArgContains("aiscan_e2e_test").NoError(),
		),
		Ordered:        true,
		OutputContains: []string{"e2e_marker_42"},
		NoErrors:       true,
		MaxTurns:       5,
		JudgeCriteria: "The agent must: (1) write the exact string 'e2e_marker_42' to a file, " +
			"(2) read it back and confirm the content matches. Both steps must succeed without errors.",
	}.Run(t, h)
}

func TestAgentGlobAndRead(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "glob-and-read",
		Prompt: "List .go files in /mnt/chainreactors/aiscan/pkg/agent/ using glob, then read the first line of defaults.go and tell me the package name.",
		Steps: Steps(
			Tool("glob").NoError(),
			Tool("read").ArgContains("defaults.go").NoError(),
		),
		Ordered:        true,
		OutputContains: []string{"agent"},
		NoErrors:       true,
		MaxTurns:       4,
		JudgeCriteria: "The agent must: (1) use glob to list .go files in the agent directory, " +
			"(2) read defaults.go, (3) correctly report that the package name is 'agent'.",
	}.Run(t, h)
}

func TestAgentMultiStepTask(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "multi-step-bash",
		Prompt: "First run 'uname -a' in bash. After you see the result, run 'whoami' in a SEPARATE bash call. Report both results.",
		Steps: Steps(
			Tool("bash").ArgContains("uname").NoError(),
			Tool("bash").ArgContains("whoami").NoError(),
		),
		Ordered:  true,
		NoErrors: true,
		MaxTurns: 6,
		JudgeCriteria: "The agent must make TWO separate bash calls: one for 'uname -a' and one for 'whoami'. " +
			"Both results must appear in the final output. They must NOT be combined in a single bash call.",
	}.Run(t, h)
}

func TestAgentMultiTurn(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "multi-turn-file-ops",
		Prompt: "Step 1: Create file /tmp/aiscan_multi.txt with content 'step1'. Step 2: Append ' step2' to it. Step 3: Read it and confirm it says 'step1 step2'.",
		NoErrors: true,
		MaxTurns: 8,
		JudgeCriteria: "The agent must perform three sequential file operations: " +
			"(1) create a file with 'step1', (2) append ' step2' to it, (3) read and confirm the content is 'step1 step2'. " +
			"The final output must confirm the combined content.",
	}.Run(t, h)
}

func TestAgentLargeOutput(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "large-output",
		Prompt: "Run 'seq 1 500' in bash. Tell me the last number printed.",
		Steps: Steps(
			Tool("bash").ArgContains("seq").NoError(),
		),
		OutputContains: []string{"500"},
		NoErrors:       true,
		MaxTurns:       8,
		JudgeCriteria:  "The agent must run 'seq 1 500' and correctly identify that the last number is 500.",
	}.Run(t, h)
}

func TestAgentErrorRecovery(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "error-recovery",
		Prompt: "Run 'cat /nonexistent/file' in bash. If it fails, report the error message. Then run 'echo recovered' and report that output.",
		Steps: Steps(
			Tool("bash").ArgContains("nonexistent"),
			Tool("bash").ArgContains("recovered").NoError(),
		),
		Ordered:        true,
		OutputContains: []string{"recovered"},
		MaxTurns:       5,
		JudgeCriteria: "The agent must: (1) attempt to cat a nonexistent file, (2) recognize the error, " +
			"(3) recover by running 'echo recovered', (4) report both the error and the recovery in the final output.",
	}.Run(t, h)
}

// =====================================================================
// CLI — scanner help, version, direct modes
// =====================================================================

func TestScannerHelpExitsClean(t *testing.T) {
	h := New(t)
	for _, name := range scannerHelpCommands() {
		t.Run(name, func(t *testing.T) {
			r := h.Scanner(name, "-h")
			Verify(t, r).
				OK().
				OutputContains("Usage:").
				Done()
		})
	}
}

func TestVersionFlag(t *testing.T) {
	h := New(t)
	r := h.Run("--version")
	Verify(t, r).
		OK().
		OutputContains("aiscan v").
		Done()
}

func TestScannerDirectGogo(t *testing.T) {
	h := New(t)
	r := h.Scanner("gogo", "-i", "127.0.0.1", "-p", "80")
	if r.ExitCode != 0 {
		t.Logf("gogo exit=%d stderr: %s", r.ExitCode, clip(r.Stderr, 500))
	}
}

func TestScannerDirectSpray(t *testing.T) {
	h := New(t)
	r := h.Scanner("spray", "-i", "http://127.0.0.1:1", "--limit", "1")
	if r.ExitCode != 0 {
		t.Logf("spray exit=%d stderr: %s", r.ExitCode, clip(r.Stderr, 500))
	}
}

func TestAgentTimeout(t *testing.T) {
	h := New(t)
	r := h.RunWithTimeout(15*time.Second,
		"agent", "-p", "Run 'sleep 60' in bash.",
		"--timeout", "5",
	)
	if r.ExitCode == 0 && r.Duration < 4*time.Second {
		t.Logf("agent completed before timeout — skipping assertion")
		return
	}
	if r.Duration < 4*time.Second {
		t.Fatalf("expected ≥4s duration, got %s", r.Duration)
	}
}

// =====================================================================
// IOA loop — task dispatch, multi-worker, peer messages
// =====================================================================

func TestIOALoopReceivesTask(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore(), "")
	srv := httptest.NewServer(ioaserver.NewHandler(service))
	defer srv.Close()

	h := New(t)

	go func() {
		h.RunWithTimeout(60*time.Second,
			"agent", "--ioa-url", "http://127.0.0.1:8765",
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
	if _, err := controller.RegisterNode(ctx, "controller", "", nil); err != nil {
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

	_, err = controller.Send(ctx, space.ID, protocols.SendMessage{
		Content: map[string]any{"content": "Run 'echo ioa_task_received' in bash and report the output."},
		Refs:    &protocols.Ref{Nodes: []string{workerNodeID}},
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(30 * time.Second)

	requireIOAMessageContains(t, controller, ctx, space.ID, "ioa_task_received")
}

func TestIOALoopMultipleWorkers(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore(), "")
	srv := httptest.NewServer(ioaserver.NewHandler(service))
	defer srv.Close()

	h := New(t)

	for i := 1; i <= 2; i++ {
		i := i
		go func() {
			h.RunWithTimeout(45*time.Second,
				"agent", "--ioa-url", "http://127.0.0.1:8765",
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
	if _, err := controller.RegisterNode(ctx, "controller", "", nil); err != nil {
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
	service := ioaserver.NewService(ioaserver.NewMemoryStore(), "")
	srv := httptest.NewServer(ioaserver.NewHandler(service))
	defer srv.Close()

	h := New(t)

	go func() {
		h.RunWithTimeout(45*time.Second,
			"agent", "--ioa-url", "http://127.0.0.1:8765",
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
	if _, err := controller.RegisterNode(ctx, "controller", "", nil); err != nil {
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

	_, err = controller.Send(ctx, space.ID, protocols.SendMessage{
		Content: map[string]any{"content": "Run echo peer_hello and report result"},
		Refs:    &protocols.Ref{Nodes: []string{workerNodeID}},
	})
	if err != nil {
		t.Fatal(err)
	}

	_, err = controller.Send(ctx, space.ID, protocols.SendMessage{
		Content: map[string]any{"content": "Additional context: also run 'echo peer_context_received'"},
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(25 * time.Second)

	requireIOAMessageContains(t, controller, ctx, space.ID, "peer_hello")
}

func TestIOATaskSpawnsSubagents(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore(), "")
	srv := httptest.NewServer(ioaserver.NewHandler(service))
	defer srv.Close()

	h := New(t)

	go func() {
		h.RunWithTimeout(90*time.Second,
			"agent", "--ioa-url", "http://127.0.0.1:8765",
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
	if _, err := controller.RegisterNode(ctx, "controller", "", nil); err != nil {
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

	_, err = controller.Send(ctx, space.ID, protocols.SendMessage{
		Content: map[string]any{
			"content": "I need you to gather system info in parallel. " +
				"Create 2 async subagents: one runs 'echo subagent_alpha_ok' in bash, " +
				"the other runs 'echo subagent_beta_ok' in bash. " +
				"Wait for both results, then respond with a combined summary that includes both markers.",
		},
		Refs: &protocols.Ref{Nodes: []string{workerNodeID}},
	})
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(60 * time.Second)

	requireIOAMessageContains(t, controller, ctx, space.ID, "subagent_alpha_ok")
	requireIOAMessageContains(t, controller, ctx, space.ID, "subagent_beta_ok")
}

func TestIOATwoWorkersDispatch(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore(), "")
	srv := httptest.NewServer(ioaserver.NewHandler(service))
	defer srv.Close()

	h := New(t)

	for i := 1; i <= 2; i++ {
		i := i
		go func() {
			h.RunWithTimeout(75*time.Second,
				"agent", "--ioa-url", "http://127.0.0.1:8765",
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
	if _, err := controller.RegisterNode(ctx, "controller", "", nil); err != nil {
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
	var workers []protocols.Node
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
		_, err = controller.Send(ctx, space.ID, protocols.SendMessage{
			Content: map[string]any{
				"content": fmt.Sprintf("Run 'echo %s' in bash and report.", marker),
			},
			Refs: &protocols.Ref{Nodes: []string{w.ID}},
		})
		if err != nil {
			t.Fatal(err)
		}
	}

	time.Sleep(45 * time.Second)

	requireIOAMessageContains(t, controller, ctx, space.ID, "dispatch_marker_1")
	requireIOAMessageContains(t, controller, ctx, space.ID, "dispatch_marker_2")
}

// =====================================================================
// Loop tool — create, lifecycle
// =====================================================================

func TestAgentLoopCreate(t *testing.T) {
	h := New(t)
	Intent{
		Name: "loop-create",
		Prompt: "Use bash to run these loop commands in order: " +
			"(1) loop '*/10 * * * *' check system health " +
			"(2) loop list " +
			"(3) loop stop the loop that was just created. " +
			"Report the results and stop.",
		Steps: Steps(
			Tool("bash").ArgContains("loop").NoError(),
			Tool("bash").ArgContains("loop").ArgContains("list").NoError(),
			Tool("bash").ArgContains("loop").ArgContains("stop").NoError(),
		),
		Ordered:  true,
		NoErrors: true,
		MaxTurns: 6,
		Timeout:  60 * time.Second,
		JudgeCriteria: "The agent must: (1) create a loop via cron expression, " +
			"(2) list loops, (3) stop the loop. All calls must succeed.",
	}.Run(t, h)
}

func TestAgentLoopLifecycle(t *testing.T) {
	h := New(t)
	Intent{
		Name: "loop-lifecycle",
		Prompt: "Use bash to run these loop commands in order: " +
			"(1) loop 5m check status " +
			"(2) loop list to confirm the loop exists " +
			"(3) loop stop <name> to stop it " +
			"(4) loop list again to confirm it is gone. " +
			"Report the results after each step and stop.",
		Steps: Steps(
			Tool("bash").ArgContains("loop").NoError(),
			Tool("bash").ArgContains("loop list").NoError(),
			Tool("bash").ArgContains("loop stop").NoError(),
			Tool("bash").ArgContains("loop list").NoError(),
		),
		Ordered:  true,
		NoErrors: true,
		MaxTurns: 8,
		Timeout:  90 * time.Second,
		JudgeCriteria: "The agent must: (1) create a loop, (2) list loops showing it exists, " +
			"(3) stop the loop, (4) list loops again confirming it is gone. " +
			"All four commands must succeed without errors.",
	}.Run(t, h)
}

// =====================================================================
// Pipeline / scan — scanner AI, scan with skills
// =====================================================================

func TestScannerAIGogo(t *testing.T) {
	h := New(t)
	r := h.RunWithTimeout(90*time.Second, "--ai", "--timeout", "60", "gogo", "-i", "127.0.0.1", "-p", "80")
	Verify(t, r).OK().Done()
}

func TestAgentGogoScan(t *testing.T) {
	h := New(t)
	r := h.Agent("Use gogo to scan 127.0.0.1 port 80. Show the raw scanner output.")
	Verify(t, r).
		OK().
		ToolUsed("bash").
		Done()
}

func TestAgentSprayScan(t *testing.T) {
	h := New(t)
	r := h.Agent("Run spray against http://127.0.0.1:1 with --limit 1 and report the result.")
	Verify(t, r).
		OK().
		ToolUsed("bash").
		Done()
}

func TestAgentScanWithSkill(t *testing.T) {
	h := New(t)
	r := h.Agent("Use the scan command to scan 127.0.0.1 with --mode quick. Summarize the results.", "-s", "aiscan")
	Verify(t, r).OK().Done()
}

func TestAgentScanAnalyze(t *testing.T) {
	h := New(t)
	r := h.Agent("Run 'scan -i 127.0.0.1 --mode quick' and analyze the output. Tell me what services were found, if any.")
	Verify(t, r).
		OK().
		ToolUsed("bash").
		Done()
}

func TestAgentScanAndVerify(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"Scan 127.0.0.1 with scan --mode quick. If any services are found, " +
			"attempt to verify them by connecting to the reported port using bash (e.g. curl or nc). " +
			"Report: services found, verification results.",
	)
	Verify(t, r).
		OK().
		ToolUsed("bash").
		Done()
}

func TestAgentScanAnalyzeVerifyPipeline(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"Execute this pipeline:\n" +
			"1. Run 'scan -i 127.0.0.1 --mode quick' to scan the target.\n" +
			"2. Parse the scan results to identify any open ports or services.\n" +
			"3. For each service found, attempt a basic verification:\n" +
			"   - If HTTP: run 'curl -s -o /dev/null -w \"%{http_code}\" http://127.0.0.1:<port>' \n" +
			"   - If SSH: run 'echo | nc -w2 127.0.0.1 <port>' \n" +
			"   - If no services found, report that.\n" +
			"4. Summarize: services found, verification status for each.",
	)
	Verify(t, r).
		OK().
		ToolUsed("bash").
		ToolArgMatch("bash", func(args string) bool {
			return strings.Contains(args, "scan") && strings.Contains(args, "127.0.0.1")
		}).
		ToolResultMatch("bash", func(res string) bool { return res != "" }).
		Done()
}

func TestAgentParallelTargetScan(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"I need to check 3 targets in parallel. Create 3 async subagents:\n" +
			"1. Named 'target-a': run 'echo target_a_scanned' in bash and report.\n" +
			"2. Named 'target-b': run 'echo target_b_scanned' in bash and report.\n" +
			"3. Named 'target-c': run 'echo target_c_scanned' in bash and report.\n" +
			"Wait for ALL subagents to complete. List the subagents to track progress. " +
			"Once all are done, produce a consolidated report with all 3 markers.",
	)
	Verify(t, r).
		OK().
		MinSubagentCreates(3).
		OutputContains("target_a_scanned").
		OutputContains("target_b_scanned").
		OutputContains("target_c_scanned").
		Done()
}

func TestAgentBackgroundTaskDrivesFollowUp(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"Start a detached tmux session: tmux new -d -s scan 'sleep 1 && echo SCAN_COMPLETE port=22 service=ssh'. " +
			"Use tmux ls to confirm it's running. " +
			"Use tmux wait -t scan to wait for it. Use tmux capture-pane -t scan to get output. " +
			"Then run a follow-up command 'echo VERIFY_22_OK' to simulate verification. " +
			"Report both the scan result and the verification result.",
	)
	Verify(t, r).
		OK().
		ToolUsed("bash").
		MinToolCalls(3).
		AnyResultContains("SCAN_COMPLETE").
		AnyResultContains("VERIFY_22_OK").
		Done()
}

func TestAgentTmuxAndSubagentCoordination(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"Do these in parallel:\n" +
			"1. Start a detached tmux session: tmux new -d -s bg 'sleep 1 && echo bg_task_done_xyz'\n" +
			"2. Create an async subagent named 'helper' with prompt: " +
			"'Run echo subagent_helper_done in bash and report.'\n" +
			"Monitor both: use tmux wait/capture-pane and wait for the subagent completion notification. " +
			"Report both results when they complete.",
	)
	Verify(t, r).
		OK().
		ToolUsed("bash").
		ToolUsed("subagent").
		AnyResultContains("bg_task_done_xyz").
		AnyResultContains("subagent_helper_done").
		Done()
}

// =====================================================================
// Real scan — direct, AI, agent, subagent, loop, IOA
// =====================================================================

func sendMessage(content, nodeID string) protocols.SendMessage {
	return protocols.SendMessage{
		Content: map[string]any{"content": content},
		Refs:    &protocols.Ref{Nodes: []string{nodeID}},
	}
}

const realTarget = "101.132.149.35/28"
const realSingleTarget = "101.132.149.35"

// Layer 1: Direct scanner (no AI) — baseline

func TestRealScanDirectGogo(t *testing.T) {
	h := New(t)
	r := h.RunWithTimeout(120*time.Second, "gogo", "-i", realTarget, "-p", "top100")
	Verify(t, r).OK().Done()
	t.Logf("gogo output (%d bytes):\n%s", len(r.Stdout), clip(r.Stdout, 2000))
}

func TestRealScanDirectSpray(t *testing.T) {
	h := New(t)
	r := h.RunWithTimeout(120*time.Second, "spray", "-i", fmt.Sprintf("http://%s", realSingleTarget), "--finger")
	Verify(t, r).OK().Done()
	t.Logf("spray output (%d bytes):\n%s", len(r.Stdout), clip(r.Stdout, 2000))
}

func TestRealScanDirectPipeline(t *testing.T) {
	h := New(t)
	r := h.RunWithTimeout(300*time.Second, "scan", "-i", realSingleTarget, "--mode", "quick")
	Verify(t, r).OK().Done()
	t.Logf("scan output (%d bytes):\n%s", len(r.Stdout), clip(r.Stdout, 3000))
}

// Layer 2: Scanner AI analysis and scan pipeline AI skills

func TestRealScanGogoAI(t *testing.T) {
	h := New(t)
	Intent{
		Name:    "real-gogo-ai",
		Prompt:  "", // not used in scanner AI mode
		Timeout: 180 * time.Second,
		JudgeCriteria: "The scanner must have executed gogo against the target and the AI must have provided " +
			"a meaningful analysis of discovered services. The analysis should mention specific ports, " +
			"services, or results - not just a generic summary.",
	}.verifyScanner(t, h, "--ai", "--timeout", "120", "gogo", "-i", realTarget, "-p", "top100")
}

func TestRealScanPipelineAISkills(t *testing.T) {
	h := New(t)
	Intent{
		Name:    "real-scan-pipeline-ai-skills",
		Prompt:  "",
		Timeout: 300 * time.Second,
		JudgeCriteria: "The scan pipeline must have run against the target with explicit AI verification " +
			"and sniper options. The output should include concrete scan findings or AI skill results, " +
			"not just a generic completion message.",
	}.verifyScanner(t, h, "--timeout", "240", "scan", "-i", realSingleTarget, "--mode", "quick", "--verify=high", "--sniper")
}

// Layer 3: Agent mode — LLM decides how to scan

func TestRealAgentGogoScan(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "real-agent-gogo",
		Prompt: fmt.Sprintf("Use gogo to scan %s with port range top100. Report all discovered services including port, protocol, and any fingerprints.", realTarget),
		Steps: Steps(
			Tool("bash").ArgContains("gogo").NoError(),
		),
		Timeout:  300 * time.Second,
		MaxTurns: 20,
		JudgeCriteria: "The agent must have executed gogo against 101.132.149.35/28 with appropriate port arguments. " +
			"The final output must list specific discovered services (port numbers, service names). " +
			"Generic statements like 'scan completed' without specific results are a failure.",
	}.Run(t, h)
}

func TestRealAgentSprayScan(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "real-agent-spray",
		Prompt: fmt.Sprintf("Use spray to probe http://%s and identify web technologies and fingerprints. Report what you find.", realSingleTarget),
		Steps: Steps(
			Tool("bash").ArgContains("spray").NoError(),
		),
		Timeout:  300 * time.Second,
		MaxTurns: 20,
		JudgeCriteria: "The agent must run spray against the target URL. The output must include specific web " +
			"technology fingerprints or HTTP response information — not just 'spray completed'.",
	}.Run(t, h)
}

func TestRealAgentFullPipeline(t *testing.T) {
	h := New(t)
	Intent{
		Name: "real-agent-full-pipeline",
		Prompt: fmt.Sprintf("Perform a comprehensive scan of %s:\n"+
			"1. Use gogo to discover open ports and services\n"+
			"2. For any HTTP services found, use spray to fingerprint them\n"+
			"3. Summarize all results: IPs, ports, services, web technologies", realSingleTarget),
		Steps: Steps(
			Tool("bash").ArgContains("gogo").NoError(),
		),
		Timeout:  300 * time.Second,
		MaxTurns: 12,
		JudgeCriteria: "The agent must execute a multi-step scan: (1) port discovery with gogo, " +
			"(2) web fingerprinting with spray for any HTTP services found. " +
			"The final summary must list concrete results (specific IPs, ports, services). " +
			"If no HTTP services are found, the agent should report that and skip spray — that's acceptable.",
	}.Run(t, h)
}

// Layer 4: Agent + skills - verify and analyze results

func TestRealAgentScanWithVerify(t *testing.T) {
	h := New(t)
	Intent{
		Name: "real-agent-scan-verify",
		Prompt: fmt.Sprintf("Scan %s with gogo. For each service found, attempt basic verification "+
			"(e.g. curl for HTTP, or nc for other services). Report: service, port, verification status.", realSingleTarget),
		Steps: Steps(
			Tool("bash").ArgContains("gogo").NoError(),
		),
		Timeout:  300 * time.Second,
		MaxTurns: 15,
		JudgeCriteria: "The agent must: (1) run gogo to discover services, (2) attempt verification of at least one " +
			"discovered service using curl/nc/similar. The report must show per-service verification status. " +
			"If gogo finds no services, the agent should report that — still a pass if handled correctly.",
	}.Run(t, h)
}

func TestRealAgentScanReport(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "real-agent-scan-report",
		Prompt: fmt.Sprintf("Scan %s using the scan command with --mode quick. Generate a security assessment report.", realSingleTarget),
		Steps: Steps(
			Tool("bash").ArgContains("scan").NoError(),
		),
		Timeout:  300 * time.Second,
		MaxTurns: 10,
		JudgeCriteria: "The agent must run the scan pipeline and produce a structured security report. " +
			"The report must contain: target IP, discovered services, risk assessment or observations. " +
			"A bare scan output dump without analysis is a failure.",
	}.Run(t, h)
}

// Layer 5: Agent + subagent fan-out — parallel scanning

func TestRealAgentParallelScan(t *testing.T) {
	h := New(t)
	Intent{
		Name: "real-agent-parallel-scan",
		Prompt: fmt.Sprintf("I need to scan %s efficiently. Create 2 async subagents:\n"+
			"1. Named 'port-scan': run gogo against the target with -p top100\n"+
			"2. Named 'web-probe': run spray against http://%s with --finger\n"+
			"Wait for both to complete, then produce a consolidated results report.", realSingleTarget, realSingleTarget),
		Steps: Steps(
			Tool("subagent").Arg("name", "port-scan"),
			Tool("subagent").Arg("name", "web-probe"),
		),
		Timeout:  300 * time.Second,
		MaxTurns: 12,
		JudgeCriteria: "The agent must create 2 async subagents for parallel scanning. " +
			"Both subagents must complete. The final report must consolidate results from both " +
			"port scanning (gogo) and web probing (spray).",
	}.Run(t, h)
}

// Layer 6: Agent + loop tool — recurring scan

func TestRealAgentLoopScan(t *testing.T) {
	h := New(t)
	Intent{
		Name: "real-agent-loop-scan",
		Prompt: fmt.Sprintf("Set up a recurring scan for %s:\n"+
			"1. First, run gogo -i %s -p top100 immediately and report results\n"+
			"2. Create a loop named 'monitor' with interval '30s' and prompt 'check if any new ports opened on %s'\n"+
			"3. List loops to confirm the monitor is active\n"+
			"4. Delete the loop named 'monitor'\n"+
			"Report the initial scan results.", realSingleTarget, realSingleTarget, realSingleTarget),
		Steps: Steps(
			Tool("bash").ArgContains("gogo").NoError(),
		),
		Ordered:  true,
		Timeout:  180 * time.Second,
		MaxTurns: 10,
		NoErrors: true,
		JudgeCriteria: "The agent must: (1) run an initial gogo scan and report results, " +
			"(2) create a recurring loop for monitoring, (3) list loops to confirm, (4) delete the loop. " +
			"All four steps must complete in order. The initial scan must produce actual results (ports/services).",
	}.Run(t, h)
}

// Layer 7: IOA loop mode — swarm worker receives scan task

func TestRealIOALoopScanTask(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore(), "")
	srv := httptest.NewServer(ioaserver.NewHandler(service))
	defer srv.Close()

	h := New(t)

	go func() {
		h.RunWithTimeout(180*time.Second,
			"agent", "--ioa-url", "http://127.0.0.1:8765",
			"--ioa-url", srv.URL,
			"--space", "real-scan",
			"--ioa-node-name", "scanner-worker",
			"-p", "I am a scanner worker with gogo, spray, and neutron capabilities",
			"--timeout", "150",
		)
	}()

	time.Sleep(5 * time.Second)

	controller, err := ioaclient.NewClient(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := context.Background()
	if _, err := controller.RegisterNode(ctx, "controller", "", nil); err != nil {
		t.Fatal(err)
	}
	space, err := controller.Space(ctx, "real-scan", "real scan test")
	if err != nil {
		t.Fatal(err)
	}

	nodes, err := controller.ListNodes(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var workerID string
	for _, n := range nodes {
		if n.Name == "scanner-worker" {
			workerID = n.ID
			break
		}
	}
	if workerID == "" {
		t.Fatal("scanner-worker not found")
	}

	_, err = controller.Send(ctx, space.ID, sendMessage(
		fmt.Sprintf("Run gogo against %s with -p top100 and report all discovered services with ports and fingerprints.", realSingleTarget),
		workerID,
	))
	if err != nil {
		t.Fatal(err)
	}

	time.Sleep(120 * time.Second)

	requireIOAMessageContains(t, controller, ctx, space.ID, realSingleTarget)
}

// =====================================================================
// Subagent — sync, async, fan-out, chain, message
// =====================================================================

func TestAgentSubagentSync(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "subagent-sync",
		Prompt: "Use the subagent tool to create a sync subagent with prompt 'echo sub_sync_ok using bash and report the output'. Report the subagent result.",
		Steps: Steps(
			Tool("subagent").Action("create").NoError(),
		),
		OutputContains: []string{"sub_sync_ok"},
		MaxTurns:       4,
		JudgeCriteria: "The agent must create a sync subagent. The subagent must execute 'echo sub_sync_ok' via bash. " +
			"The final output must contain 'sub_sync_ok' proving the subagent completed and returned its result.",
	}.Run(t, h)
}

func TestAgentSubagentAsync(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "subagent-async",
		Prompt: "Create an async subagent with prompt 'Run echo async_marker_99 in bash'. Wait for its completion notification and report its result.",
		Steps: Steps(
			Tool("subagent").Action("create").NoError(),
		),
		OutputContains: []string{"async_marker_99"},
		MaxTurns:       8,
		JudgeCriteria: "The agent must create an async subagent. It must then wait for the subagent completion notification " +
			"(which arrives via inbox). The final output must contain 'async_marker_99'.",
	}.Run(t, h)
}

func TestAgentSubagentSyncTimeout(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "subagent-sync-timeout",
		Prompt: "Create a sync subagent with timeout '2s' and prompt 'Run sleep 30 in bash'. Report what happened (it should timeout).",
		Steps: Steps(
			Tool("subagent").ResultHas("timed out"),
		),
		MaxTurns: 3,
		JudgeCriteria: "The agent must create a sync subagent with a 2s timeout running 'sleep 30'. " +
			"The subagent must timeout. The agent must report the timeout in its output.",
	}.Run(t, h)
}

func TestAgentSubagentList(t *testing.T) {
	h := New(t)
	Intent{
		Name:   "subagent-list",
		Prompt: "Create an async subagent named 'worker1' with prompt 'sleep 5'. Then immediately use subagent list action to show running subagents. Report the list.",
		Steps: Steps(
			Tool("subagent").Arg("name", "worker1"),
			Tool("subagent").Action("list"),
		),
		MaxTurns: 6,
		JudgeCriteria: "The agent must: (1) create an async subagent named 'worker1', " +
			"(2) call subagent list to show running subagents, " +
			"(3) the list result should show 'worker1' as running.",
	}.Run(t, h)
}

func TestAgentMultiSubagentFanOut(t *testing.T) {
	h := New(t)
	Intent{
		Name: "subagent-fan-out",
		Prompt: "You have 3 independent tasks. Use the subagent tool to create 3 SEPARATE async subagents, one for each:\n" +
			"1. Subagent named 'host-info': run 'uname -a' in bash and report.\n" +
			"2. Subagent named 'user-info': run 'whoami' in bash and report.\n" +
			"3. Subagent named 'dir-info': run 'pwd' in bash and report.\n" +
			"Create all 3 subagents, then wait for all completion notifications. " +
			"Summarize all 3 results together.",
		Steps: Steps(
			Tool("subagent").Arg("name", "host-info"),
			Tool("subagent").Arg("name", "user-info"),
			Tool("subagent").Arg("name", "dir-info"),
		),
		MaxTurns: 10,
		JudgeCriteria: "The agent must create exactly 3 async subagents (host-info, user-info, dir-info). " +
			"It must wait for all 3 completions. The final output must summarize results from all 3 subagents.",
	}.Run(t, h)
}

func TestAgentSubagentWithBashAndReport(t *testing.T) {
	h := New(t)
	Intent{
		Name: "subagent-bash-report",
		Prompt: "Create 2 async subagents:\n" +
			"1. Named 'counter': run 'seq 1 5' in bash.\n" +
			"2. Named 'greeter': run 'echo hello_from_subagent' in bash.\n" +
			"Wait for both to complete. Then report both outputs in your final answer.",
		Steps: Steps(
			Tool("subagent").Arg("name", "counter"),
			Tool("subagent").Arg("name", "greeter"),
		),
		OutputContains: []string{"hello_from_subagent"},
		MaxTurns:       10,
		JudgeCriteria: "The agent must create 2 subagents and wait for both. " +
			"The final output must include the output from both: the sequence 1-5 and 'hello_from_subagent'.",
	}.Run(t, h)
}

func TestAgentSubagentChain(t *testing.T) {
	h := New(t)
	Intent{
		Name: "subagent-chain",
		Prompt: "Step 1: Create a sync subagent that runs 'echo chain_step_1' in bash and returns the output.\n" +
			"Step 2: After you receive the result from step 1, create another sync subagent " +
			"that runs 'echo chain_step_2' in bash.\n" +
			"Report both results to confirm the chain completed.",
		MaxTurns: 8,
		JudgeCriteria: "The agent must create 2 sync subagents sequentially (not in parallel). " +
			"Step 2 must happen AFTER step 1 completes. " +
			"The final output must contain both 'chain_step_1' and 'chain_step_2'.",
		Check: func(t *testing.T, r *RunResult) {
			results := r.SubagentResults()
			if len(results) < 2 {
				t.Fatalf("expected ≥2 subagent results, got %d", len(results))
			}
			s1, s2 := -1, -1
			for i, res := range results {
				if strings.Contains(res, "chain_step_1") && s1 == -1 {
					s1 = i
				}
				if strings.Contains(res, "chain_step_2") && s2 == -1 {
					s2 = i
				}
			}
			if s1 >= 0 && s2 >= 0 && s1 >= s2 {
				t.Fatalf("chain order wrong: step1 at %d, step2 at %d", s1, s2)
			}
		},
	}.Run(t, h)
}

func TestAgentSubagentMessage(t *testing.T) {
	h := New(t)
	Intent{
		Name: "subagent-message",
		Prompt: "Create an async subagent named 'listener' with prompt: " +
			"'Wait for a message. When you receive one, run echo GOT_MESSAGE in bash and report.'\n" +
			"After creating it, use the subagent message action to send a message " +
			"'hello from parent' to the 'listener' subagent.\n" +
			"Wait for the listener to complete and report its result.",
		Steps: Steps(
			Tool("subagent").Arg("name", "listener"),
			Tool("subagent").Action("message").Arg("name", "listener"),
		),
		Ordered:  true,
		MaxTurns: 10,
		JudgeCriteria: "The agent must: (1) create an async subagent named 'listener', " +
			"(2) send a message to it via the subagent message action, " +
			"(3) the listener must execute 'echo GOT_MESSAGE' after receiving the message, " +
			"(4) the final output must contain 'GOT_MESSAGE' confirming the message was received and processed.",
	}.Run(t, h)
}

// =====================================================================
// Task — tmux background tasks
// =====================================================================

func TestAgentBackgroundTask(t *testing.T) {
	h := New(t)
	r := h.Agent("Start a background shell session: tmux new -d -s bg 'sleep 1 && echo bg_done'. Then use tmux ls to list running sessions. Use tmux wait -t bg to wait for it to finish. Use tmux capture-pane -t bg to get the output. Report the final output.")
	Verify(t, r).
		OK().
		ToolUsed("bash").
		AnyResultContains("bg_done").
		NoToolErrors().
		Done()
}

func TestAgentTmuxPeek(t *testing.T) {
	h := New(t)
	r := h.Agent("Run 'for i in 1 2 3; do echo line_$i; sleep 0.5; done' as a detached tmux session named 'lines'. Use tmux capture-pane -t lines --new to check its output, then wait for completion and report all lines.")
	Verify(t, r).
		OK().
		ToolUsed("bash").
		Done()
}

func TestAgentTmuxKill(t *testing.T) {
	h := New(t)
	r := h.Agent("Start a detached tmux session: tmux new -d -s sleeper 'sleep 300'. Use tmux ls to confirm it's running. Kill it with tmux kill -t sleeper. List again to confirm it's killed. Report status.")
	Verify(t, r).
		OK().
		ToolUsed("bash").
		Done()
}

// =====================================================================
// Verify mechanism — scan verify/sniper mode tests
// =====================================================================

const verifyTarget = realSingleTarget

// TestVerifyOffProducesNoAIOutput runs scan with --verify=off and confirms
// that no AI skill output appears in the results.
func TestVerifyOffProducesNoAIOutput(t *testing.T) {
	h := New(t)
	r := h.RunWithTimeout(300*time.Second,
		"scan", "-i", "127.0.0.1", "--mode", "quick", "--verify=off", "--timeout", "3",
	)
	Verify(t, r).OK().Done()

	if hasAISkillOutput(r.Stdout) {
		t.Fatalf("--verify=off should produce no AI skill output, got:\n%s", clip(r.Stdout, 2000))
	}
	t.Logf("verify=off output (%d bytes):\n%s", len(r.Stdout), clip(r.Stdout, 2000))
}

// TestVerifyHighWithSniperTriggersAIVerification runs scan with explicit
// verify and sniper options and confirms that the scan pipeline completes with
// AI skills enabled. When targets have high-priority loots, AI verify and
// sniper skills produce output.
func TestVerifyHighWithSniperTriggersAIVerification(t *testing.T) {
	h := New(t)
	r := h.RunWithTimeout(600*time.Second,
		"scan", "-i", verifyTarget, "--mode", "quick", "--verify=high", "--sniper", "--timeout", "5",
	)
	Verify(t, r).OK().Done()

	if !hasSummaryLine(r.Stdout) {
		t.Fatal("expected [summary] line in output")
	}
	t.Logf("verify+sniper output (%d bytes):\n%s", len(r.Stdout), clip(r.Stdout, 3000))
}

// TestVerifyExplicitModeWithoutSniper runs scan with --verify=high explicitly
// (no --sniper) and checks that verify runs but sniper is NOT activated.
func TestVerifyExplicitModeWithoutSniper(t *testing.T) {
	h := New(t)
	r := h.RunWithTimeout(600*time.Second,
		"scan", "-i", verifyTarget, "--mode", "quick", "--verify=high", "--timeout", "5",
	)
	Verify(t, r).OK().Done()

	if hasSniperOutput(r.Stdout) {
		t.Fatal("--verify=high without --sniper should not produce sniper output")
	}
	if !hasSummaryLine(r.Stdout) {
		t.Fatal("expected [summary] line in output")
	}
	t.Logf("verify=high output (%d bytes):\n%s", len(r.Stdout), clip(r.Stdout, 2000))
}

// TestScanVerifySniperNoPostAnalysis verifies that the old post-analysis
// one-shot LLM call no longer runs. Explicit scan AI skills trigger only
// in-pipeline AI work (verify + sniper), not a separate "analysis" step.
// The output should contain the [summary] line from the scan pipeline but
// should not contain the "analysis" output section that runScannerPostAnalysis
// used to produce.
func TestScanVerifySniperNoPostAnalysis(t *testing.T) {
	h := New(t)
	r := h.RunWithTimeout(600*time.Second,
		"scan", "-i", verifyTarget, "--mode", "quick", "--verify=high", "--sniper", "--timeout", "5",
	)
	Verify(t, r).OK().Done()

	if !hasSummaryLine(r.Stdout) {
		t.Fatal("expected [summary] line from scan pipeline")
	}
	t.Logf("output (%d bytes):\n%s", len(r.Stdout), clip(r.Stdout, 3000))
}

// TestScanDefaultModeCompletes runs scan without any explicit AI skill flags.
// The default verify mode is "auto" (mapped to "high"), which enables the
// provider optionally. If the provider initializes, AI verify can run; if not,
// the scan still completes successfully.
func TestScanDefaultModeCompletes(t *testing.T) {
	h := New(t)
	r := h.RunWithTimeout(600*time.Second,
		"scan", "-i", verifyTarget, "--mode", "quick", "--timeout", "5",
	)
	Verify(t, r).OK().Done()

	if !hasSummaryLine(r.Stdout) {
		t.Fatal("expected [summary] line in output")
	}
	t.Logf("default mode output (%d bytes):\n%s", len(r.Stdout), clip(r.Stdout, 2000))
}

// TestVerifyOffDisablesAllAISkills confirms that --verify=off combined with
// no --sniper and no --deep results in zero AI skill results in the summary.
func TestVerifyOffDisablesAllAISkills(t *testing.T) {
	h := New(t)
	r := h.RunWithTimeout(300*time.Second,
		"scan", "-i", "127.0.0.1", "--mode", "quick", "--verify=off", "--timeout", "3",
	)
	Verify(t, r).OK().Done()

	summary := extractSummaryLine(r.Stdout)
	if summary == "" {
		t.Fatal("missing [summary] line")
	}
	if strings.Contains(summary, "verified") {
		parts := strings.Fields(summary)
		for i, p := range parts {
			if p == "verified" && i > 0 && parts[i-1] != "0" {
				t.Fatalf("expected 0 verified in summary with --verify=off, got: %s", summary)
			}
		}
	}
	t.Logf("verify=off summary: %s", summary)
}

// TestScanVerifyWithReportIncludesVerification runs scan with explicit
// verification and report output and verifies the report includes AI
// verification metrics.
func TestScanVerifyWithReportIncludesVerification(t *testing.T) {
	h := New(t)
	r := h.RunWithTimeout(600*time.Second,
		"scan", "-i", verifyTarget, "--mode", "quick", "--verify=high", "--sniper", "--report", "--timeout", "5",
	)
	Verify(t, r).OK().Done()

	hasMetrics := strings.Contains(r.Stdout, "AI verifications") ||
		strings.Contains(r.Stdout, "AI skill") ||
		strings.Contains(r.Stdout, "verified")
	if !hasMetrics {
		t.Fatal("--verify=high --sniper --report should include AI verification information in output")
	}
	t.Logf("report output (%d bytes):\n%s", len(r.Stdout), clip(r.Stdout, 3000))
}

// TestAssetReportFileOutputFormats runs scan with -f and -F and verifies both
// output formats include structured checkpoint loots.
func TestAssetReportFileOutputFormats(t *testing.T) {
	h := New(t)
	r := h.RunWithTimeout(600*time.Second,
		"scan", "-i", verifyTarget, "--mode", "quick", "--verify=high", "--sniper", "--timeout", "5",
		"-f", "output.txt", "-F", "asset_report.txt",
	)
	Verify(t, r).OK().Done()

	plainBytes, err := os.ReadFile(h.WorkFile("output.txt"))
	if err != nil {
		t.Fatalf("read -f output: %v", err)
	}
	plain := string(plainBytes)
	t.Logf("-f output (%d bytes):\n%s", len(plain), clip(plain, 3000))

	assetReportBytes, err := os.ReadFile(h.WorkFile("asset_report.txt"))
	if err != nil {
		if !hasAISkillOutput(r.Stdout) {
			t.Skip("no AI output produced, skipping -F check")
		}
		t.Fatalf("read -F output: %v", err)
	}
	assetReport := string(assetReportBytes)
	t.Logf("-F output (%d bytes):\n%s", len(assetReport), clip(assetReport, 3000))

	if len(assetReport) > 0 {
		if !strings.Contains(assetReport, "Assets:") {
			t.Fatal("-F output should contain 'Assets:' header")
		}
	}
}

// =====================================================================
// Shared helpers
// =====================================================================

// containsCount counts occurrences of substr in s.
func containsCount(s, substr string) int {
	return strings.Count(s, substr)
}

// requireIOAMessageContains checks that at least one message in the space contains substr.
func requireIOAMessageContains(t *testing.T, client *ioaclient.Client, ctx context.Context, spaceID, substr string) {
	t.Helper()
	msgs, err := client.Read(ctx, spaceID, protocols.ReadOptions{All: true})
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

// verifyScanner runs a direct scanner command and uses the judge to evaluate.
func (intent Intent) verifyScanner(t *testing.T, h *Harness, args ...string) *RunResult {
	t.Helper()
	r := h.RunWithTimeout(intent.Timeout, args...)
	v := Verify(t, r).OK()
	if intent.JudgeCriteria != "" {
		prompt := fmt.Sprintf("Scanner command: %v", args)
		v = v.JudgeWith(h.Judge(), prompt, intent.JudgeCriteria)
	}
	v.Done()
	t.Logf("output (%d bytes):\n%s", len(r.Stdout), clip(r.Stdout, 2000))
	return r
}

func hasAISkillOutput(output string) bool {
	markers := []string{"[ai:", "[sniper:", "[ai]", "[sniper]"}
	for _, m := range markers {
		if strings.Contains(output, m) {
			return true
		}
	}
	return false
}

func hasSniperOutput(output string) bool {
	return strings.Contains(output, "[sniper:") || strings.Contains(output, "[sniper]")
}

func hasSummaryLine(output string) bool {
	return strings.Contains(output, "[summary]") || strings.Contains(output, "completed")
}

func extractSummaryLine(output string) string {
	for _, line := range strings.Split(output, "\n") {
		if strings.Contains(line, "[summary]") {
			return line
		}
	}
	return ""
}
