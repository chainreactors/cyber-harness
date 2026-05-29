//go:build e2e

package harness

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
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

	var monitorDone chan struct{}
	if h.monitor != nil && needsEvents {
		monitorDone = make(chan struct{})
		go h.monitor.run(eventsFile, monitorDone)
	}

	start := time.Now()
	err := cmd.Run()
	duration := time.Since(start)

	if monitorDone != nil {
		close(monitorDone)
		time.Sleep(50 * time.Millisecond)
	}

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

// --- convenience runners ---

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

func clip(s string, maxLen int) string {
	s = strings.TrimSpace(s)
	if len(s) <= maxLen {
		return s
	}
	return s[:maxLen] + "... (truncated)"
}
