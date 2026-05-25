//go:build e2e

package harness

import (
	"testing"
	"time"
)

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
			return containsStr(args, "scan") && containsStr(args, "127.0.0.1")
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
		"Start a background bash task: 'sleep 1 && echo SCAN_COMPLETE port=22 service=ssh'. " +
			"While it runs, list tasks to confirm it's active. " +
			"Wait for it to finish. When you see the output, " +
			"run a follow-up command 'echo VERIFY_22_OK' to simulate verification. " +
			"Report both the scan result and the verification result.",
	)
	Verify(t, r).
		OK().
		ToolUsed("task").
		ToolUsed("bash").
		MinToolCalls(3).
		AnyResultContains("SCAN_COMPLETE").
		AnyResultContains("VERIFY_22_OK").
		ToolSequence("bash", "task").
		Done()
}

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
	Verify(t, r).
		OK().
		ToolUsed("bash").
		ToolUsed("subagent").
		AnyResultContains("bg_task_done_xyz").
		AnyResultContains("subagent_helper_done").
		Done()
}
