//go:build e2e

package harness

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

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
	monitor *Monitor
}

func (h *Harness) WithMonitor(out ...io.Writer) *Harness {
	w := io.Writer(os.Stderr)
	if len(out) > 0 {
		w = out[0]
	}
	h.monitor = NewMonitor(w)
	return h
}

func New(t *testing.T) *Harness {
	t.Helper()

	baseURL := os.Getenv("AISCAN_TEST_BASE_URL")
	apiKey := os.Getenv("AISCAN_TEST_API_KEY")
	model := os.Getenv("AISCAN_TEST_MODEL")

	if apiKey == "" {
		t.Skip("AISCAN_TEST_API_KEY not set, skipping e2e test")
	}
	if baseURL == "" {
		baseURL = "https://api.deepseek.com"
	}
	if model == "" {
		model = "deepseek-v4-pro"
	}

	cachedExeOnce.Do(func() {
		cachedExe, cachedExeErr = buildOnce(t)
	})
	if cachedExeErr != nil {
		t.Fatalf("build aiscan: %v", cachedExeErr)
	}

	h := &Harness{
		t:       t,
		exe:     cachedExe,
		workDir: t.TempDir(),
		baseURL: baseURL,
		apiKey:  apiKey,
		model:   model,
		timeout: 180 * time.Second,
	}
	if os.Getenv("AISCAN_MONITOR") != "" {
		h.monitor = NewMonitor(os.Stderr)
	}
	return h
}

func buildOnce(t *testing.T) (string, error) {
	t.Helper()
	dir, err := os.MkdirTemp("", "aiscan-e2e-*")
	if err != nil {
		return "", err
	}
	exeName := "aiscan-e2e"
	if runtime.GOOS == "windows" {
		exeName += ".exe"
	}
	exe := filepath.Join(dir, exeName)
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

	var fullArgs []string
	switch {
	case len(args) > 0 && args[0] == "agent":
		fullArgs = h.agentCLIArgs(args[1:]...)
	case len(args) == 1 && args[0] == "--version":
		fullArgs = []string{"--no-color", "--quiet", "--version"}
	default:
		fullArgs = append(h.llmArgs(), "--no-color", "--quiet")
		fullArgs = append(fullArgs, args...)
	}

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

	exitCode := processExitCode(err)

	result := &RunResult{
		Stdout:   stdout.String(),
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Duration: duration,
	}

	h.t.Logf("ran: aiscan %s (exit=%d, duration=%s, turns=%d, tools=%d)",
		strings.Join(args, " "), exitCode, duration.Round(time.Millisecond),
		result.Turns(), len(result.ToolCalls()))
	if exitCode != 0 {
		h.t.Logf("stderr: %s", clip(stderr.String(), 2000))
	}

	return result
}

func (h *Harness) WorkFile(name string) string {
	return filepath.Join(h.workDir, name)
}

// --- convenience runners ---

func (h *Harness) Agent(prompt string, extraArgs ...string) *RunResult {
	h.t.Helper()
	return h.AgentWithTimeout(h.timeout, prompt, extraArgs...)
}

func (h *Harness) AgentWithTimeout(timeout time.Duration, prompt string, extraArgs ...string) *RunResult {
	h.t.Helper()

	fullArgs := h.agentCLIArgs(extraArgs...)

	ctx, cancel := context.WithTimeout(context.Background(), timeout)
	defer cancel()

	cmd := exec.CommandContext(ctx, h.exe, fullArgs...)
	cmd.Dir = h.workDir
	cmd.Env = append(os.Environ(), "AISCAN_EVENTS_FILE=-")

	stdin, err := cmd.StdinPipe()
	if err != nil {
		h.t.Fatalf("agent stdin pipe: %v", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		h.t.Fatalf("agent stdout pipe: %v", err)
	}
	var stderr bytes.Buffer
	cmd.Stderr = &stderr

	start := time.Now()
	if err := cmd.Start(); err != nil {
		h.t.Fatalf("start aiscan agent: %v", err)
	}

	taskID := h.t.Name()
	request := webproto.Message{Type: "chat", TaskID: taskID, Data: prompt}
	writeErr := json.NewEncoder(stdin).Encode(request)
	closeErr := stdin.Close()
	if writeErr != nil || closeErr != nil {
		cancel()
	}

	stream, streamErr := consumeAgentStream(stdout, taskID, h.monitor)
	if streamErr != nil {
		cancel()
	}
	waitErr := cmd.Wait()
	duration := time.Since(start)

	exitCode := processExitCode(waitErr)
	if writeErr != nil {
		exitCode = -1
		fmt.Fprintf(&stderr, "write webproto stdin: %v\n", writeErr)
	} else if closeErr != nil {
		exitCode = -1
		fmt.Fprintf(&stderr, "close webproto stdin: %v\n", closeErr)
	}
	if streamErr != nil {
		exitCode = -1
		fmt.Fprintf(&stderr, "read webproto stdout: %v\n", streamErr)
	}

	result := &RunResult{
		Stdout:   stream.Output,
		Stderr:   stderr.String(),
		ExitCode: exitCode,
		Duration: duration,
		Events:   stream.Events,
	}

	h.t.Logf("ran: aiscan agent (exit=%d, duration=%s, turns=%d, tools=%d)",
		exitCode, duration.Round(time.Millisecond), result.Turns(), len(result.ToolCalls()))
	if exitCode != 0 {
		h.t.Logf("stderr: %s", clip(stderr.String(), 2000))
	}

	return result
}

func (h *Harness) AgentWithInput(prompt string, inputs []string, extraArgs ...string) *RunResult {
	h.t.Helper()
	args := make([]string, 0, len(inputs)*2+len(extraArgs))
	for _, input := range inputs {
		args = append(args, "-i", input)
	}
	args = append(args, extraArgs...)
	task := fmt.Sprintf("%s\n\nTargets:\n%s", prompt, config.FormatInputs(inputs))
	return h.Agent(task, args...)
}

func (h *Harness) agentCLIArgs(extraArgs ...string) []string {
	args := []string{"--no-color", "--quiet", "agent"}
	args = append(args, h.llmArgs()...)
	return append(args, extraArgs...)
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

// --- helpers ---

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

func processExitCode(err error) int {
	if err == nil {
		return 0
	}
	if exitErr, ok := err.(*exec.ExitError); ok {
		return exitErr.ExitCode()
	}
	return -1
}

func clip(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "... (truncated)"
}
