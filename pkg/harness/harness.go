//go:build e2e

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type RunResult struct {
	Stdout   string
	Stderr   string
	ExitCode int
	Duration time.Duration
	Events   []AgentEvent
}

type AgentEvent struct {
	Type       string `json:"type"`
	Turn       int    `json:"turn,omitempty"`
	ToolName   string `json:"tool_name,omitempty"`
	ToolCallID string `json:"tool_call_id,omitempty"`
	Args       string `json:"arguments,omitempty"`
	Result     string `json:"result,omitempty"`
	IsError    bool   `json:"is_error,omitempty"`
	Error      string `json:"error,omitempty"`
}

func (r *RunResult) OK() bool { return r.ExitCode == 0 }

func (r *RunResult) Output() string { return strings.TrimSpace(r.Stdout) }

func (r *RunResult) Combined() string { return r.Stdout + r.Stderr }

func (r *RunResult) ContainsOutput(substr string) bool {
	return strings.Contains(r.Stdout, substr) || strings.Contains(r.Stderr, substr)
}

func (r *RunResult) ToolCalls() []AgentEvent {
	var calls []AgentEvent
	for _, e := range r.Events {
		if e.Type == "tool_execution_end" {
			calls = append(calls, e)
		}
	}
	return calls
}

func (r *RunResult) HasToolCall(name string) bool {
	for _, e := range r.ToolCalls() {
		if e.ToolName == name {
			return true
		}
	}
	return false
}

func (r *RunResult) ToolCallsNamed(name string) []AgentEvent {
	var out []AgentEvent
	for _, e := range r.ToolCalls() {
		if e.ToolName == name {
			out = append(out, e)
		}
	}
	return out
}

func (r *RunResult) Turns() int {
	max := 0
	for _, e := range r.Events {
		if e.Turn > max {
			max = e.Turn
		}
	}
	return max
}

func (r *RunResult) LoopCalls() []AgentEvent {
	return r.ToolCallsNamed("loop")
}

func (r *RunResult) LoopCreateCount() int {
	n := 0
	for _, e := range r.LoopCalls() {
		if strings.Contains(e.Args, `"create"`) {
			n++
		}
	}
	return n
}

func (r *RunResult) SubagentCalls() []AgentEvent {
	return r.ToolCallsNamed("subagent")
}

func (r *RunResult) SubagentCreateCount() int {
	n := 0
	for _, e := range r.SubagentCalls() {
		if !strings.Contains(e.Args, `"list"`) && !strings.Contains(e.Args, `"kill"`) && !strings.Contains(e.Args, `"message"`) {
			n++
		}
	}
	return n
}

func (r *RunResult) ToolResultContains(toolName, substr string) bool {
	for _, e := range r.ToolCallsNamed(toolName) {
		if strings.Contains(e.Result, substr) {
			return true
		}
	}
	return false
}

func (r *RunResult) ToolArgsContains(toolName, substr string) bool {
	for _, e := range r.ToolCallsNamed(toolName) {
		if strings.Contains(e.Args, substr) {
			return true
		}
	}
	return false
}

func (r *RunResult) SubagentCreateArgs() []string {
	var args []string
	for _, e := range r.SubagentCalls() {
		if !strings.Contains(e.Args, `"list"`) && !strings.Contains(e.Args, `"kill"`) && !strings.Contains(e.Args, `"message"`) {
			args = append(args, e.Args)
		}
	}
	return args
}

func (r *RunResult) SubagentResults() []string {
	var results []string
	for _, e := range r.SubagentCalls() {
		if !strings.Contains(e.Args, `"list"`) && !strings.Contains(e.Args, `"kill"`) && !strings.Contains(e.Args, `"message"`) {
			results = append(results, e.Result)
		}
	}
	return results
}

func (r *RunResult) AllToolResults() string {
	var sb strings.Builder
	for _, e := range r.ToolCalls() {
		sb.WriteString(e.Result)
		sb.WriteByte('\n')
	}
	return sb.String()
}

func (r *RunResult) ToolCallSequence() []string {
	var names []string
	for _, e := range r.ToolCalls() {
		names = append(names, e.ToolName)
	}
	return names
}

var (
	cachedExe     string
	cachedExeOnce sync.Once
	cachedExeErr  error
)

type Harness struct {
	t       *testing.T
	exe     string
	workDir string
	baseURL string
	apiKey  string
	model   string
	timeout time.Duration
}

func New(t *testing.T) *Harness {
	t.Helper()

	baseURL := envOrDefault("AISCAN_TEST_BASE_URL", "https://api.deepseek.com")
	apiKey := envOrDefault("AISCAN_TEST_API_KEY", "REMOVED_CREDENTIAL")
	model := envOrDefault("AISCAN_TEST_MODEL", "deepseek-v4-pro")

	if apiKey == "" {
		t.Skip("AISCAN_TEST_API_KEY not set, skipping e2e test")
	}

	cachedExeOnce.Do(func() {
		cachedExe, cachedExeErr = buildOnce(t)
	})
	if cachedExeErr != nil {
		t.Fatalf("build aiscan: %v", cachedExeErr)
	}

	return &Harness{
		t:       t,
		exe:     cachedExe,
		workDir: t.TempDir(),
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		timeout: 180 * time.Second,
	}
}

func buildOnce(t *testing.T) (string, error) {
	t.Helper()
	dir, err := os.MkdirTemp("", "aiscan-e2e-*")
	if err != nil {
		return "", err
	}
	exe := filepath.Join(dir, "aiscan-e2e")
	args := []string{"build", "-tags", buildTags(), "-o", exe, "./cmd/aiscan"}
	cmd := exec.Command("go", args...)
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		return "", fmt.Errorf("%v\n%s", err, out)
	}
	return exe, nil
}

func (h *Harness) llmArgs() []string {
	return []string{
		"--base-url", h.baseURL,
		"--api-key", h.apiKey,
		"--model", h.model,
	}
}

func (h *Harness) Run(args ...string) *RunResult {
	h.t.Helper()
	return h.RunWithTimeout(h.timeout, args...)
}

func (h *Harness) RunWithTimeout(timeout time.Duration, args ...string) *RunResult {
	h.t.Helper()

	eventsFile := filepath.Join(h.workDir, fmt.Sprintf("events-%d.jsonl", time.Now().UnixNano()))

	fullArgs := append(h.llmArgs(), "--no-color", "--quiet")

	needsEvents := false
	for _, a := range args {
		if a == "agent" {
			needsEvents = true
			break
		}
	}
	if needsEvents {
		fullArgs = append(fullArgs, "--events-file", eventsFile)
	}
	fullArgs = append(fullArgs, args...)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, h.exe, fullArgs...)
	cmd.Dir = h.workDir

	var stdout, stderr bytes.Buffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	exitCode := 0
	if err != nil {
		if exitErr, ok := err.(*exec.ExitError); ok {
			exitCode = exitErr.ExitCode()
		} else if ctx.Err() != nil {
			exitCode = -1
		}
	}

	result := &RunResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Duration: duration,
	}

	if needsEvents {
		result.Events = loadEvents(eventsFile)
	}

	h.t.Logf("ran: aiscan %s (exit=%d, duration=%s, turns=%d, tools=%d)",
		strings.Join(args, " "), exitCode, duration.Round(time.Millisecond),
		result.Turns(), len(result.ToolCalls()))
	if exitCode != 0 {
		h.t.Logf("stderr: %s", clip(stderr.String(), 2000))
	}

	return result
}

func (h *Harness) Agent(prompt string, extraArgs ...string) *RunResult {
	h.t.Helper()
	args := []string{"agent", "-p", prompt}
	args = append(args, extraArgs...)
	return h.Run(args...)
}

func (h *Harness) AgentWithInput(prompt string, inputs []string, extraArgs ...string) *RunResult {
	h.t.Helper()
	args := []string{"agent", "-p", prompt}
	for _, input := range inputs {
		args = append(args, "-i", input)
	}
	args = append(args, extraArgs...)
	return h.Run(args...)
}

func (h *Harness) Scanner(name string, scannerArgs ...string) *RunResult {
	h.t.Helper()
	args := []string{name}
	args = append(args, scannerArgs...)
	return h.Run(args...)
}

func (h *Harness) ScannerAI(name string, scannerArgs ...string) *RunResult {
	h.t.Helper()
	args := []string{"--ai", name}
	args = append(args, scannerArgs...)
	return h.Run(args...)
}

func (h *Harness) RequireOK(r *RunResult) {
	h.t.Helper()
	if !r.OK() {
		h.t.Fatalf("command failed (exit=%d):\nstdout: %s\nstderr: %s",
			r.ExitCode, clip(r.Stdout, 1000), clip(r.Stderr, 1000))
	}
}

func (h *Harness) RequireContains(r *RunResult, substr string) {
	h.t.Helper()
	if !r.ContainsOutput(substr) {
		h.t.Fatalf("output missing %q:\nstdout: %s\nstderr: %s",
			substr, clip(r.Stdout, 1000), clip(r.Stderr, 1000))
	}
}

func (h *Harness) RequireToolResult(r *RunResult, toolName, substr string) {
	h.t.Helper()
	if !r.ToolResultContains(toolName, substr) {
		var results []string
		for _, e := range r.ToolCallsNamed(toolName) {
			results = append(results, clip(e.Result, 300))
		}
		h.t.Fatalf("%s tool results missing %q:\nresults: %s", toolName, substr, strings.Join(results, "\n---\n"))
	}
}

func (h *Harness) RequireAnyResult(r *RunResult, substr string) {
	h.t.Helper()
	all := r.AllToolResults()
	if !strings.Contains(all, substr) && !r.ContainsOutput(substr) {
		h.t.Fatalf("no tool result or output contains %q", substr)
	}
}

func repoRoot(t *testing.T) string {
	t.Helper()
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}

func envOrDefault(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

func loadEvents(path string) []AgentEvent {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil
	}
	var events []AgentEvent
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		var e AgentEvent
		if json.Unmarshal([]byte(line), &e) == nil {
			events = append(events, e)
		}
	}
	return events
}

func clip(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "... (truncated)"
}
