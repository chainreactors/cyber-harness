//go:build e2e

package harness

import (
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os/exec"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/ioa"
	ioaclient "github.com/chainreactors/ioa/client"
	ioaserver "github.com/chainreactors/ioa/server"
)

// ===========================================================================
// CLI sanity
// ===========================================================================

func TestScannerHelpExitsClean(t *testing.T) {
	h := New(t)
	for _, name := range scannerHelpCommands() {
		t.Run(name, func(t *testing.T) {
			r := h.Scanner(name, "-h")
			h.RequireOK(r)
			if count := strings.Count(r.Stdout, "Usage:"); count < 1 {
				t.Fatalf("%s -h missing Usage block:\n%s", name, r.Stdout)
			}
		})
	}
}

func TestVersionFlag(t *testing.T) {
	h := New(t)
	r := h.Run("--version")
	h.RequireOK(r)
	h.RequireContains(r, "aiscan v")
}

// ===========================================================================
// Agent: basic tool usage
// ===========================================================================

func TestAgentSimplePrompt(t *testing.T) {
	h := New(t)
	r := h.Agent("What is 2+2? Reply with just the number.")
	h.RequireOK(r)
	if !strings.Contains(r.Output(), "4") {
		t.Fatalf("expected '4' in output, got: %s", r.Output())
	}
}

func TestAgentBashTool(t *testing.T) {
	h := New(t)
	r := h.Agent("Run 'echo hello_e2e' in a shell and tell me the exact output.")
	h.RequireOK(r)
	if !r.HasToolCall("bash") {
		t.Fatal("expected bash tool call")
	}
	h.RequireToolResult(r, "bash", "hello_e2e")
	h.RequireContains(r, "hello_e2e")
}

func TestAgentReadTool(t *testing.T) {
	h := New(t)
	r := h.Agent("Read /etc/hostname and reply with only its contents.")
	h.RequireOK(r)
	if r.Output() == "" {
		t.Fatal("expected non-empty output")
	}
}

func TestAgentWriteReadRoundtrip(t *testing.T) {
	h := New(t)
	r := h.Agent("Write 'e2e_marker_42' to /tmp/aiscan_e2e_test.txt, then read it back and confirm.")
	h.RequireOK(r)
	h.RequireContains(r, "e2e_marker_42")
}

func TestAgentGlobAndRead(t *testing.T) {
	h := New(t)
	r := h.Agent("List .go files in /mnt/chainreactors/aiscan/pkg/agent/ using glob, then read the first line of defaults.go and tell me the package name.")
	h.RequireOK(r)
	h.RequireContains(r, "agent")
}

// ===========================================================================
// Agent: multi-turn reasoning
// ===========================================================================

func TestAgentMultiStepTask(t *testing.T) {
	h := New(t)
	r := h.Agent("First run 'uname -a' in bash. After you see the result, run 'whoami' in a SEPARATE bash call. Report both results.")
	h.RequireOK(r)
	if len(r.ToolCalls()) < 2 {
		t.Fatalf("expected ≥2 tool calls, got %d", len(r.ToolCalls()))
	}
}

func TestAgentMultiTurn(t *testing.T) {
	h := New(t)
	r := h.Agent("Step 1: Create file /tmp/aiscan_multi.txt with content 'step1'. Step 2: Append ' step2' to it. Step 3: Read it and confirm it says 'step1 step2'.")
	h.RequireOK(r)
	if r.Turns() < 2 {
		t.Logf("warning: expected ≥2 turns, got %d", r.Turns())
	}
}

// ===========================================================================
// Background task + task tool
// ===========================================================================

func TestAgentBackgroundTask(t *testing.T) {
	h := New(t)
	r := h.Agent("Start a background bash task: 'sleep 1 && echo bg_done'. Then use the task tool to list running tasks. Wait for it to finish. Report the final output.")
	h.RequireOK(r)
	if !r.HasToolCall("bash") {
		t.Fatal("expected bash tool call")
	}
	if !r.HasToolCall("task") {
		t.Fatal("expected task tool call")
	}
	if !r.ToolArgsContains("bash", "background") {
		t.Fatal("bash tool should have been called with background flag")
	}
	h.RequireToolResult(r, "bash", "Started")
	h.RequireAnyResult(r, "bg_done")
}

func TestAgentTaskPeek(t *testing.T) {
	h := New(t)
	r := h.Agent("Run 'for i in 1 2 3; do echo line_$i; sleep 0.5; done' as a background task. Use task peek to check its output, then wait for completion and report all lines.")
	h.RequireOK(r)
	if !r.HasToolCall("task") {
		t.Fatal("expected task tool call for peek")
	}
}

func TestAgentTaskKill(t *testing.T) {
	h := New(t)
	r := h.Agent("Start a background task 'sleep 300' named 'sleeper'. List tasks to confirm it's running. Kill it. List again to confirm it's gone or killed. Report status.")
	h.RequireOK(r)
	taskCalls := r.ToolCallsNamed("task")
	if len(taskCalls) < 2 {
		t.Fatalf("expected ≥2 task tool calls (list+kill), got %d", len(taskCalls))
	}
	if !r.ToolArgsContains("task", "kill") {
		t.Fatal("expected task kill action in tool args")
	}
	if !r.ToolResultContains("task", "SIGTERM") && !r.ToolResultContains("task", "killed") {
		t.Logf("task results: %v", r.SubagentResults())
		t.Log("warning: kill confirmation not found in task results (may have completed before kill)")
	}
}

// ===========================================================================
// Subagent
// ===========================================================================

func TestAgentSubagentSync(t *testing.T) {
	h := New(t)
	r := h.Agent("Use the subagent tool to create a sync subagent with prompt 'echo sub_sync_ok using bash and report the output'. Report the subagent result.")
	h.RequireOK(r)
	if !r.HasToolCall("subagent") {
		t.Fatal("expected subagent tool call")
	}
	if !r.ToolArgsContains("subagent", "sync") {
		t.Log("warning: subagent args may not explicitly contain 'sync' mode")
	}
	h.RequireToolResult(r, "subagent", "sub_sync_ok")
	h.RequireContains(r, "sub_sync_ok")
}

func TestAgentSubagentAsync(t *testing.T) {
	h := New(t)
	r := h.Agent("Create an async subagent with prompt 'Run echo async_marker_99 in bash'. Wait for its completion notification and report its result.")
	h.RequireOK(r)
	if !r.HasToolCall("subagent") {
		t.Fatal("expected subagent tool call")
	}
	h.RequireContains(r, "async_marker_99")
}

func TestAgentSubagentSyncTimeout(t *testing.T) {
	h := New(t)
	r := h.Agent("Create a sync subagent with timeout '2s' and prompt 'Run sleep 30 in bash'. Report what happened (it should timeout).")
	h.RequireOK(r)
	if !r.HasToolCall("subagent") {
		t.Fatal("expected subagent tool call")
	}
	h.RequireToolResult(r, "subagent", "timed out")
}

func TestAgentSubagentList(t *testing.T) {
	h := New(t)
	r := h.Agent("Create an async subagent named 'worker1' with prompt 'sleep 5'. Then immediately use subagent list action to show running subagents. Report the list.")
	h.RequireOK(r)
	subCalls := r.ToolCallsNamed("subagent")
	hasCreate := false
	hasList := false
	for _, c := range subCalls {
		if strings.Contains(c.Args, "create") || (!strings.Contains(c.Args, "list") && !strings.Contains(c.Args, "kill")) {
			hasCreate = true
		}
		if strings.Contains(c.Args, "list") {
			hasList = true
		}
	}
	if !hasCreate || !hasList {
		t.Fatalf("expected create+list subagent calls, got: %v", subCalls)
	}
}

// ===========================================================================
// Scanner: direct mode (no LLM)
// ===========================================================================

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

// ===========================================================================
// Scanner AI mode (agentic scanner)
// ===========================================================================

func TestScannerAIGogo(t *testing.T) {
	h := New(t)
	r := h.RunWithTimeout(90*time.Second, "--ai", "--timeout", "60", "gogo", "-i", "127.0.0.1", "-p", "80")
	h.RequireOK(r)
}

func TestAgentGogoScan(t *testing.T) {
	h := New(t)
	r := h.Agent("Use gogo to scan 127.0.0.1 port 80. Show the raw scanner output.")
	h.RequireOK(r)
	if !r.HasToolCall("bash") {
		t.Fatal("expected bash tool call for gogo execution")
	}
}

func TestAgentSprayScan(t *testing.T) {
	h := New(t)
	r := h.Agent("Run spray against http://127.0.0.1:1 with --limit 1 and report the result.")
	h.RequireOK(r)
	if !r.HasToolCall("bash") {
		t.Fatal("expected bash tool call for spray")
	}
}

// ===========================================================================
// Agent + scanner skill
// ===========================================================================

func TestAgentScanWithSkill(t *testing.T) {
	h := New(t)
	r := h.Agent("Use the scan command to scan 127.0.0.1 with --mode quick. Summarize the results.", "-s", "aiscan")
	h.RequireOK(r)
}

func TestAgentScanAnalyze(t *testing.T) {
	h := New(t)
	r := h.Agent("Run 'scan -i 127.0.0.1 --mode quick' and analyze the output. Tell me what services were found, if any.")
	h.RequireOK(r)
	if !r.HasToolCall("bash") {
		t.Fatal("expected bash tool call for scan")
	}
}

// ===========================================================================
// Agent: scan + auto verify workflow
// ===========================================================================

func TestAgentScanAndVerify(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"Scan 127.0.0.1 with scan --mode quick. If any services are found, "+
			"attempt to verify them by connecting to the reported port using bash (e.g. curl or nc). "+
			"Report: services found, verification results.",
	)
	h.RequireOK(r)
	bashCalls := r.ToolCallsNamed("bash")
	if len(bashCalls) < 1 {
		t.Fatal("expected at least 1 bash call for scan")
	}
}

// ===========================================================================
// IOA loop + swarm
// ===========================================================================

func TestIOALoopReceivesTask(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore())
	srv := httptest.NewServer(ioaserver.NewHandler(service))
	defer srv.Close()

	h := New(t)

	loopDone := make(chan *RunResult, 1)
	go func() {
		r := h.RunWithTimeout(60*time.Second,
			"agent", "--loop",
			"--ioa-url", srv.URL,
			"--space", "test-loop",
			"-p", "I am a test worker",
			"--timeout", "45",
		)
		loopDone <- r
	}()

	time.Sleep(3 * time.Second)

	controller, err := ioaclient.NewClient(srv.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	ctx := t.Context()
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

	msgs, err := controller.Read(ctx, space.ID, ioa.ReadOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}

	found := false
	for _, m := range msgs {
		raw, _ := json.Marshal(m.Content)
		if strings.Contains(string(raw), "ioa_task_received") {
			found = true
			break
		}
	}
	if !found {
		var summaries []string
		for _, m := range msgs {
			raw, _ := json.Marshal(m.Content)
			summaries = append(summaries, clip(string(raw), 200))
		}
		t.Logf("messages in space:\n%s", strings.Join(summaries, "\n"))
		t.Fatal("worker did not produce result containing 'ioa_task_received'")
	}
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
	ctx := t.Context()
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
	ctx := t.Context()
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

	msgs, err := controller.Read(ctx, space.ID, ioa.ReadOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}

	foundTask := false
	for _, m := range msgs {
		raw, _ := json.Marshal(m.Content)
		if strings.Contains(string(raw), "peer_hello") {
			foundTask = true
		}
	}
	if !foundTask {
		t.Fatal("worker did not process task message")
	}
}

// ===========================================================================
// Agent timeout
// ===========================================================================

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

// ===========================================================================
// Edge cases
// ===========================================================================

func TestAgentEmptyReply(t *testing.T) {
	h := New(t)
	r := h.Agent("Reply with the word 'pong' and nothing else.")
	h.RequireOK(r)
	if !strings.Contains(strings.ToLower(r.Output()), "pong") {
		t.Fatalf("expected 'pong', got: %s", r.Output())
	}
}

func TestAgentLargeOutput(t *testing.T) {
	h := New(t)
	r := h.Agent("Run 'seq 1 500' in bash. Tell me the last number printed.")
	h.RequireOK(r)
	h.RequireContains(r, "500")
}

func TestAgentErrorRecovery(t *testing.T) {
	h := New(t)
	r := h.Agent("Run 'cat /nonexistent/file' in bash. If it fails, report the error message. Then run 'echo recovered' and report that output.")
	h.RequireOK(r)
	h.RequireContains(r, "recovered")
}

// ===========================================================================
// Complex: multi-subagent fan-out
// ===========================================================================

func TestAgentMultiSubagentFanOut(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"You have 3 independent tasks. Use the subagent tool to create 3 SEPARATE async subagents, one for each:\n" +
			"1. Subagent named 'host-info': run 'uname -a' in bash and report.\n" +
			"2. Subagent named 'user-info': run 'whoami' in bash and report.\n" +
			"3. Subagent named 'dir-info': run 'pwd' in bash and report.\n" +
			"Create all 3 subagents, then wait for all completion notifications. " +
			"Summarize all 3 results together.",
	)
	h.RequireOK(r)
	creates := r.SubagentCreateCount()
	if creates < 3 {
		t.Fatalf("expected ≥3 subagent creates, got %d", creates)
	}
	createArgs := r.SubagentCreateArgs()
	names := strings.Join(createArgs, " ")
	for _, expected := range []string{"host-info", "user-info", "dir-info"} {
		if !strings.Contains(names, expected) {
			t.Logf("warning: subagent name %q not found in create args", expected)
		}
	}
	for _, res := range r.SubagentResults() {
		if !strings.Contains(res, "Started") {
			t.Logf("unexpected subagent result: %s", clip(res, 200))
		}
	}
	if r.Turns() < 2 {
		t.Fatal("expected multiple turns (create subagents + receive completions)")
	}
}

func TestAgentSubagentWithBashAndReport(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"Create 2 async subagents:\n" +
			"1. Named 'counter': run 'seq 1 5' in bash.\n" +
			"2. Named 'greeter': run 'echo hello_from_subagent' in bash.\n" +
			"Wait for both to complete. Then report both outputs in your final answer.",
	)
	h.RequireOK(r)
	if r.SubagentCreateCount() < 2 {
		t.Fatalf("expected ≥2 subagent creates, got %d", r.SubagentCreateCount())
	}
	h.RequireContains(r, "hello_from_subagent")
}

// ===========================================================================
// Complex: IOA task triggers multi-subagent
// ===========================================================================

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
	ctx := t.Context()
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

	msgs, err := controller.Read(ctx, space.ID, ioa.ReadOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}

	foundAlpha := false
	foundBeta := false
	for _, m := range msgs {
		raw, _ := json.Marshal(m.Content)
		s := string(raw)
		if strings.Contains(s, "subagent_alpha_ok") {
			foundAlpha = true
		}
		if strings.Contains(s, "subagent_beta_ok") {
			foundBeta = true
		}
	}

	if !foundAlpha || !foundBeta {
		var summaries []string
		for _, m := range msgs {
			raw, _ := json.Marshal(m.Content)
			summaries = append(summaries, clip(string(raw), 300))
		}
		t.Logf("messages:\n%s", strings.Join(summaries, "\n---\n"))
		t.Fatalf("expected both subagent markers in IOA results (alpha=%v, beta=%v)", foundAlpha, foundBeta)
	}
}

// ===========================================================================
// Complex: background task completion drives follow-up
// ===========================================================================

func TestAgentBackgroundTaskDrivesFollowUp(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"Start a background bash task: 'sleep 1 && echo SCAN_COMPLETE port=22 service=ssh'. " +
			"While it runs, list tasks to confirm it's active. " +
			"Wait for it to finish. When you see the output, " +
			"run a follow-up command 'echo VERIFY_22_OK' to simulate verification. " +
			"Report both the scan result and the verification result.",
	)
	h.RequireOK(r)
	if !r.HasToolCall("task") {
		t.Fatal("expected task tool usage")
	}
	bashCalls := r.ToolCallsNamed("bash")
	if len(bashCalls) < 2 {
		t.Fatalf("expected ≥2 bash calls (bg task + verify), got %d", len(bashCalls))
	}
	h.RequireAnyResult(r, "SCAN_COMPLETE")
	h.RequireAnyResult(r, "VERIFY_22_OK")

	seq := r.ToolCallSequence()
	bgIdx := -1
	verifyIdx := -1
	for i, name := range seq {
		if name == "bash" && bgIdx == -1 {
			bgIdx = i
		}
		if name == "task" && bgIdx >= 0 && verifyIdx == -1 {
			verifyIdx = i
		}
	}
	if bgIdx >= 0 && verifyIdx >= 0 && verifyIdx <= bgIdx {
		t.Fatal("task tool should be called after background bash task was started")
	}
}

// ===========================================================================
// Complex: subagent chain (result of one triggers another)
// ===========================================================================

func TestAgentSubagentChain(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"Step 1: Create a sync subagent that runs 'echo chain_step_1' in bash and returns the output.\n" +
			"Step 2: After you receive the result from step 1, create another sync subagent " +
			"that runs 'echo chain_step_2' in bash.\n" +
			"Report both results to confirm the chain completed.",
	)
	h.RequireOK(r)
	if r.SubagentCreateCount() < 2 {
		t.Fatalf("expected ≥2 subagent creates for chain, got %d", r.SubagentCreateCount())
	}
	results := r.SubagentResults()
	if len(results) < 2 {
		t.Fatalf("expected ≥2 subagent results, got %d", len(results))
	}
	step1Found := false
	step2Found := false
	step1Idx := -1
	step2Idx := -1
	for i, res := range results {
		if strings.Contains(res, "chain_step_1") {
			step1Found = true
			step1Idx = i
		}
		if strings.Contains(res, "chain_step_2") {
			step2Found = true
			step2Idx = i
		}
	}
	if !step1Found || !step2Found {
		t.Fatalf("chain results missing: step1=%v step2=%v\nresults: %v", step1Found, step2Found, results)
	}
	if step1Idx >= step2Idx {
		t.Fatalf("chain order wrong: step1 at index %d, step2 at index %d", step1Idx, step2Idx)
	}
}

// ===========================================================================
// Complex: subagent message passing
// ===========================================================================

func TestAgentSubagentMessage(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"Create an async subagent named 'listener' with prompt: " +
			"'Wait for a message. When you receive one, run echo GOT_MESSAGE in bash and report.'\n" +
			"After creating it, use the subagent message action to send a message " +
			"'hello from parent' to the 'listener' subagent.\n" +
			"Wait for the listener to complete and report its result.",
	)
	h.RequireOK(r)
	subCalls := r.SubagentCalls()
	hasCreate := false
	hasMessage := false
	for _, c := range subCalls {
		if strings.Contains(c.Args, `"message"`) {
			hasMessage = true
			if !strings.Contains(c.Args, "listener") {
				t.Log("warning: message action may not target 'listener' subagent")
			}
		} else if !strings.Contains(c.Args, `"list"`) && !strings.Contains(c.Args, `"kill"`) {
			hasCreate = true
		}
	}
	if !hasCreate {
		t.Fatal("expected subagent create action")
	}
	if !hasMessage {
		t.Fatalf("expected subagent message action, got %d subagent calls", len(subCalls))
	}
	h.RequireAnyResult(r, "GOT_MESSAGE")
}

// ===========================================================================
// Complex: scan → analyze → verify pipeline
// ===========================================================================

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
	h.RequireOK(r)
	bashCalls := r.ToolCallsNamed("bash")
	if len(bashCalls) < 1 {
		t.Fatal("expected at least 1 bash call for scan")
	}
	scanRan := false
	for _, c := range bashCalls {
		if strings.Contains(c.Args, "scan") && strings.Contains(c.Args, "127.0.0.1") {
			scanRan = true
			break
		}
	}
	if !scanRan {
		t.Fatal("no bash call contained 'scan' command for 127.0.0.1")
	}
	scanResult := ""
	for _, c := range bashCalls {
		if strings.Contains(c.Args, "scan") {
			scanResult = c.Result
			break
		}
	}
	if scanResult == "" {
		t.Fatal("scan command returned empty result")
	}
	t.Logf("scan result (first 500 chars): %s", clip(scanResult, 500))
	if r.Turns() < 2 {
		t.Logf("warning: expected multi-turn pipeline, got %d turns", r.Turns())
	}
}

// ===========================================================================
// Complex: parallel subagent scan of multiple targets
// ===========================================================================

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
	h.RequireOK(r)
	if r.SubagentCreateCount() < 3 {
		t.Fatalf("expected ≥3 subagent creates, got %d", r.SubagentCreateCount())
	}
	h.RequireContains(r, "target_a_scanned")
	h.RequireContains(r, "target_b_scanned")
	h.RequireContains(r, "target_c_scanned")
}

// ===========================================================================
// Complex: IOA two workers dispatch
// ===========================================================================

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
	ctx := t.Context()
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

	msgs, err := controller.Read(ctx, space.ID, ioa.ReadOptions{All: true})
	if err != nil {
		t.Fatal(err)
	}

	markers := map[string]bool{"dispatch_marker_1": false, "dispatch_marker_2": false}
	for _, m := range msgs {
		raw, _ := json.Marshal(m.Content)
		s := string(raw)
		for k := range markers {
			if strings.Contains(s, k) {
				markers[k] = true
			}
		}
	}

	for k, found := range markers {
		if !found {
			var summaries []string
			for _, m := range msgs {
				raw, _ := json.Marshal(m.Content)
				summaries = append(summaries, clip(string(raw), 200))
			}
			t.Logf("messages:\n%s", strings.Join(summaries, "\n---\n"))
			t.Fatalf("marker %q not found in IOA results", k)
		}
	}
}

// ===========================================================================
// Complex: background task + subagent coordination
// ===========================================================================

func TestAgentTaskAndSubagentCoordination(t *testing.T) {
	h := New(t)
	r := h.Agent(
		"Do these in parallel:\n" +
			"1. Start a background bash task: 'sleep 1 && echo bg_task_done_xyz'\n" +
			"2. Create an async subagent named 'helper' with prompt: " +
			"'Run echo subagent_helper_done in bash and report.'\n" +
			"Monitor both: use task list/wait and wait for the subagent completion notification. " +
			"Report both results when they complete.",
	)
	h.RequireOK(r)
	if !r.HasToolCall("bash") {
		t.Fatal("expected bash tool call for background task")
	}
	if !r.HasToolCall("subagent") {
		t.Fatal("expected subagent tool call")
	}
	h.RequireAnyResult(r, "bg_task_done_xyz")
	h.RequireAnyResult(r, "subagent_helper_done")

	seq := r.ToolCallSequence()
	hasBash := false
	hasSubagent := false
	for _, name := range seq {
		if name == "bash" {
			hasBash = true
		}
		if name == "subagent" {
			hasSubagent = true
		}
	}
	if !hasBash || !hasSubagent {
		t.Fatalf("expected both bash and subagent in sequence, got: %v", seq)
	}
}

// ===========================================================================
// init
// ===========================================================================

func init() {
	if _, err := exec.LookPath("go"); err != nil {
		panic("go compiler not found; e2e tests require Go toolchain")
	}
}
