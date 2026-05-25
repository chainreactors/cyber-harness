//go:build e2e

package harness

import (
	"context"
	"fmt"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/chainreactors/ioa"
	ioaclient "github.com/chainreactors/ioa/client"
	ioaserver "github.com/chainreactors/ioa/server"
)

func sendMessage(content, nodeID string) ioa.SendMessage {
	return ioa.SendMessage{
		Content: map[string]any{"content": content},
		Refs:    &ioa.Ref{Nodes: []string{nodeID}},
	}
}

const realTarget = "101.132.149.35/28"
const realSingleTarget = "101.132.149.35"

// =====================================================================
// Layer 1: Direct scanner (no AI) — baseline
// =====================================================================

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

// =====================================================================
// Layer 2: Scanner + AI post-analysis (--ai flag)
// =====================================================================

func TestRealScanGogoAI(t *testing.T) {
	h := New(t)
	Intent{
		Name:    "real-gogo-ai",
		Prompt:  "", // not used in scanner AI mode
		Timeout: 180 * time.Second,
		JudgeCriteria: "The scanner must have executed gogo against the target and the AI must have provided " +
			"a meaningful analysis of discovered services. The analysis should mention specific ports, " +
			"services, or findings — not just a generic summary.",
	}.verifyScanner(t, h, "--ai", "--timeout", "120", "gogo", "-i", realTarget, "-p", "top100")
}

func TestRealScanPipelineAI(t *testing.T) {
	h := New(t)
	Intent{
		Name:    "real-scan-pipeline-ai",
		Prompt:  "",
		Timeout: 300 * time.Second,
		JudgeCriteria: "The scan pipeline must have run against the target. The AI analysis should identify " +
			"specific open ports, running services, and any notable findings from the scan results.",
	}.verifyScanner(t, h, "--ai", "--timeout", "240", "scan", "-i", realSingleTarget, "--mode", "quick")
}

// =====================================================================
// Layer 3: Agent mode — LLM decides how to scan
// =====================================================================

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
			"Generic statements like 'scan completed' without specific findings are a failure.",
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
			"3. Summarize all findings: IPs, ports, services, web technologies", realSingleTarget),
		Steps: Steps(
			Tool("bash").ArgContains("gogo").NoError(),
		),
		Timeout:  300 * time.Second,
		MaxTurns: 12,
		JudgeCriteria: "The agent must execute a multi-step scan: (1) port discovery with gogo, " +
			"(2) web fingerprinting with spray for any HTTP services found. " +
			"The final summary must list concrete findings (specific IPs, ports, services). " +
			"If no HTTP services are found, the agent should report that and skip spray — that's acceptable.",
	}.Run(t, h)
}

// =====================================================================
// Layer 4: Agent + skills — verify and analyze findings
// =====================================================================

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

// =====================================================================
// Layer 5: Agent + subagent fan-out — parallel scanning
// =====================================================================

func TestRealAgentParallelScan(t *testing.T) {
	h := New(t)
	Intent{
		Name: "real-agent-parallel-scan",
		Prompt: fmt.Sprintf("I need to scan %s efficiently. Create 2 async subagents:\n"+
			"1. Named 'port-scan': run gogo against the target with -p top100\n"+
			"2. Named 'web-probe': run spray against http://%s with --finger\n"+
			"Wait for both to complete, then produce a consolidated findings report.", realSingleTarget, realSingleTarget),
		Steps: Steps(
			Tool("subagent").Arg("name", "port-scan"),
			Tool("subagent").Arg("name", "web-probe"),
		),
		Timeout:  300 * time.Second,
		MaxTurns: 12,
		JudgeCriteria: "The agent must create 2 async subagents for parallel scanning. " +
			"Both subagents must complete. The final report must consolidate findings from both " +
			"port scanning (gogo) and web probing (spray).",
	}.Run(t, h)
}

// =====================================================================
// Layer 6: Agent + loop tool — recurring scan
// =====================================================================

func TestRealAgentLoopScan(t *testing.T) {
	h := New(t)
	Intent{
		Name: "real-agent-loop-scan",
		Prompt: fmt.Sprintf("Set up a recurring scan for %s:\n"+
			"1. First, run gogo -i %s -p top100 immediately and report findings\n"+
			"2. Create a loop named 'monitor' with interval '30s' and prompt 'check if any new ports opened on %s'\n"+
			"3. List loops to confirm the monitor is active\n"+
			"4. Delete the loop named 'monitor'\n"+
			"Report the initial scan results.", realSingleTarget, realSingleTarget, realSingleTarget),
		Steps: Steps(
			Tool("bash").ArgContains("gogo").NoError(),
			Tool("loop").Action("create").Arg("name", "monitor"),
			Tool("loop").Action("list"),
			Tool("loop").Action("delete").Arg("name", "monitor"),
		),
		Ordered:  true,
		Timeout:  180 * time.Second,
		MaxTurns: 10,
		NoErrors: true,
		JudgeCriteria: "The agent must: (1) run an initial gogo scan and report findings, " +
			"(2) create a recurring loop for monitoring, (3) list loops to confirm, (4) delete the loop. " +
			"All four steps must complete in order. The initial scan must produce actual findings (ports/services).",
	}.Run(t, h)
}

// =====================================================================
// Layer 7: IOA loop mode — swarm worker receives scan task
// =====================================================================

func TestRealIOALoopScanTask(t *testing.T) {
	service := ioaserver.NewService(ioaserver.NewMemoryStore())
	srv := httptest.NewServer(ioaserver.NewHandler(service))
	defer srv.Close()

	h := New(t)

	go func() {
		h.RunWithTimeout(180*time.Second,
			"agent", "--loop",
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
	if _, err := controller.RegisterNode(ctx, "controller", nil); err != nil {
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
// helpers
// =====================================================================

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
