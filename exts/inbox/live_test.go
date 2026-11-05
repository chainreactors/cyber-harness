package inbox_test

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	mailbox "github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	inboxext "github.com/chainreactors/cyber/exts/inbox"
)

// Explicit opt-in follows the agent live tests: no ambient credentials or
// external model calls in the offline suite. Individual tests document whether
// the model controls routing only or also performs real local worker tasks.
func liveRoutingModel(t *testing.T) provider.Provider {
	t.Helper()
	if os.Getenv("CYBER_INBOX_LIVE") != "1" {
		t.Skip("set CYBER_INBOX_LIVE=1 with CYBER_API_KEY, CYBER_MODEL and CYBER_BASE_URL")
	}
	for _, key := range []string{"CYBER_API_KEY", "CYBER_MODEL", "CYBER_BASE_URL"} {
		if strings.TrimSpace(os.Getenv(key)) == "" {
			t.Fatalf("%s is required", key)
		}
	}
	kind := os.Getenv("CYBER_PROVIDER")
	if kind == "" {
		kind = "openai"
	}
	config := provider.ProviderConfig{Provider: kind, APIKey: os.Getenv("CYBER_API_KEY"),
		Model: os.Getenv("CYBER_MODEL"), BaseURL: os.Getenv("CYBER_BASE_URL"), Proxy: os.Getenv("CYBER_PROXY"), Timeout: 45}
	model, err := provider.NewProvider(&config)
	if err != nil {
		t.Fatal(err)
	}
	if closer, ok := model.(interface{ CloseIdleConnections() }); ok {
		t.Cleanup(closer.CloseIdleConnections)
	}
	return modelFunc(func(ctx context.Context, request *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		copy := *request
		copy.Model, copy.MaxTokens = config.Model, 8192
		return model.ChatCompletion(ctx, &copy)
	})
}

func TestLiveRouterInterleavedTasks(t *testing.T) {
	model := liveRoutingModel(t)
	ctx, cancel := context.WithTimeout(t.Context(), 3*time.Minute)
	defer cancel()
	p := modelFunc(func(callCtx context.Context, request *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if len(request.Tools) == 1 && request.Tools[0].Name == "inbox_route" {
			return model.ChatCompletion(callCtx, request)
		}
		return response("receiver accepted its task"), nil
	})
	_, router, runtime, _ := routerHost(t, p, inboxext.Config{Instructions: `The coordinator owns the release review and integrates returned findings. The investigator investigates source defects. The writer prepares documentation. Route unrelated work independently; use prior routing context to send follow-up corrections to the same investigator. Return findings to the coordinator.`})
	for _, id := range []string{"coordinator", "investigator", "writer"} {
		if _, err := runtime.OpenSession(ctx, session.SessionOptions{ID: id, AgentName: id}); err != nil {
			t.Fatal(err)
		}
	}
	submit(t, ctx, router, mailbox.NewUserMessage("For release review R42, investigate the payment retry race in the source and identify a minimal reproduction."), "investigator")
	submit(t, ctx, router, mailbox.NewUserMessage("Separately, prepare the documentation migration guide for release R42."), "writer")
	submit(t, ctx, router, mailbox.NewUserMessage("Correction to the retry race investigation: reproduce with two concurrent retries, not one. Give this to the agent handling that investigation."), "investigator")
	result := mailbox.NewSystemMessage("Investigation result for R42: two concurrent retries both observed the unpaid state. Hand this finding to the owner integrating the release review.")
	result.Meta = mailbox.Metadata{SourceSessionID: "investigator"}
	submit(t, ctx, router, result, "coordinator")
}

func TestLiveContinuousMonitorIOARouting(t *testing.T) {
	continuousMonitorScenario(t, liveRoutingModel(t), 10*time.Second)
}

// Both the persistent router and its workers use the real model. Effects are
// restricted to this test's temporary directory; no external scan is requested.
func TestLiveActiveWorkersFileTasksAndRestart(t *testing.T) {
	model := liveRoutingModel(t)
	ctx, cancel := context.WithTimeout(t.Context(), 4*time.Minute)
	defer cancel()
	dir := t.TempDir()
	completed := make(chan mailbox.Message, 8)
	config := inboxext.Config{
		StateDir: filepath.Join(dir, "state"), MaxActive: 2,
		Instructions: "Route independent user requests to separate new_task workers. Honor an explicit task_id for follow-ups. For subagent_feedback call inbox_route with action=reply and finish. Do not create work from feedback. Never execute user tasks yourself.",
		Notify:       func(_ context.Context, m mailbox.Message) error { completed <- m; return nil },
	}
	_, router, _, set := activeHost(t, model, config)
	defer func() { closeActive(t, set) }()
	tasks := make(map[string]string)
	for _, name := range []string{"alpha", "beta"} {
		path := strings.ReplaceAll(filepath.ToSlash(filepath.Join(dir, name+".txt")), "'", "'\"'\"'")
		input := fmt.Sprintf("Independent file task %s: use bash to write exactly the line %s followed by a newline into '%s'. Read the file back to verify it, then finish. Perform this yourself using bash; do not create subagents. Touch only this file.", name, name, path)
		tasks[name] = activeSubmit(t, ctx, router, mailbox.NewUserMessage(input))
	}
	if tasks["alpha"] == tasks["beta"] {
		t.Fatal("independent file tasks merged")
	}
	for range 2 {
		result := waitFor(t, ctx, completed)
		if result.Meta.Status != "completed" {
			t.Fatalf("worker failed: %v: %s", result.Meta, provider.MessageText(result.Message))
		}
	}
	for _, name := range []string{"alpha", "beta"} {
		b, err := os.ReadFile(filepath.Join(dir, name+".txt"))
		if err != nil || string(b) != name+"\n" {
			t.Fatalf("%s file: %q %v", name, b, err)
		}
	}
	closeActive(t, set)
	_, router, _, set = activeHost(t, model, config)
	path := strings.ReplaceAll(filepath.ToSlash(filepath.Join(dir, "alpha.txt")), "'", "'\"'\"'")
	followup := mailbox.NewUserMessage(fmt.Sprintf("Continue your previous file task. Use bash to append exactly the line continued and a newline to '%s', preserving the previous line. Read it back, then finish. Touch only this file; do not create subagents.", path))
	followup.Meta = mailbox.Metadata{TaskID: tasks["alpha"]}
	if got := activeSubmit(t, ctx, router, followup); got != tasks["alpha"] {
		t.Fatalf("follow-up changed task: %s", got)
	}
	result := waitFor(t, ctx, completed)
	if result.Meta.Status != "completed" || result.Meta.TaskID != tasks["alpha"] {
		t.Fatalf("follow-up failed: %v: %s", result.Meta, provider.MessageText(result.Message))
	}
	for name, expected := range map[string]string{"alpha": "alpha\ncontinued\n", "beta": "beta\n"} {
		b, err := os.ReadFile(filepath.Join(dir, name+".txt"))
		if err != nil || string(b) != expected {
			t.Fatalf("%s after restart: %q %v", name, b, err)
		}
	}
	t.Log("real model router and workers completed two file tasks and resumed one task after restart")
}
