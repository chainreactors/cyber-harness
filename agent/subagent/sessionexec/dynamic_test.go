package sessionexec

import (
	"context"
	"errors"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/subagent"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
	"github.com/chainreactors/cyber/core/resource"
	coretool "github.com/chainreactors/cyber/core/tool"
)

func TestCatalogAndNamedDefaultsAreDynamic(t *testing.T) {
	parent := agent.NewAgent(agent.Config{SessionID: "parent", Provider: &scriptedProvider{}, Loop: taskLoop(func(_ context.Context, cfg agent.Config) (*agent.Result, error) {
		return &agent.Result{Output: cfg.Model}, nil
	})})
	tool := newSubagentTestTool(t, parent.Cfg)
	var prepares atomic.Int32
	h, err := tool.registry.Add(subagent.Subagent{Name: "review", Description: "review changes", Prepare: func(_ context.Context, cfg agent.Config, input subagent.Input) (agent.Config, string, error) {
		prepares.Add(1)
		cfg.Model = "review-model"
		return cfg, input.Prompt, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err := tool.Execute(t.Context(), `{"action":"catalog"}`)
	if err != nil || !strings.Contains(coretool.ResultText(catalog), "review changes") {
		t.Fatalf("catalog=%v err=%v", catalog, err)
	}
	ctx := operation.ContextWithInvocation(agent.ContextWithToolAgentConfig(t.Context(), parent.Cfg), operation.Invocation{CallID: "spawn"})
	result, err := tool.Execute(ctx, `{"name":"review","label":"one","prompt":"inspect"}`)
	if err != nil || !strings.Contains(coretool.ResultText(result), "review-model") || prepares.Load() != 1 {
		t.Fatalf("result=%v prepares=%d err=%v", result, prepares.Load(), err)
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	catalog, err = tool.Execute(t.Context(), `{"action":"catalog"}`)
	if err != nil || strings.Contains(coretool.ResultText(catalog), "review changes") {
		t.Fatalf("stale catalog=%v err=%v", catalog, err)
	}
	if _, err := tool.Execute(ctx, `{"name":"review","prompt":"inspect"}`); err == nil {
		t.Fatal("unknown name fell back to anonymous")
	}
}

func TestWithdrawalWaitsForFinalRecordAndNotification(t *testing.T) {
	reg := hooks.New()
	started, recording, release := make(chan struct{}), make(chan struct{}), make(chan struct{})
	unblock := sync.OnceFunc(func() { close(release) })
	defer unblock()
	agenthooks.SessionEnd.On(reg, "record", func(_ context.Context, ev agenthooks.SessionEvent) (struct{}, error) {
		if ev.ParentToolCallID != "" {
			close(recording)
			<-release
		}
		return struct{}{}, nil
	})
	parent := agent.NewAgent(agent.Config{SessionID: "parent", Hooks: reg, Provider: &scriptedProvider{}, Loop: taskLoop(func(ctx context.Context, _ agent.Config) (*agent.Result, error) {
		close(started)
		<-ctx.Done()
		return nil, ctx.Err()
	})})
	tool := newSubagentTestTool(t, parent.Cfg)
	h, err := tool.registry.Add(subagent.Subagent{Name: "worker", DefaultMode: subagent.Async, Prepare: func(_ context.Context, cfg agent.Config, input subagent.Input) (agent.Config, string, error) {
		return cfg, input.Prompt, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	ctx := operation.ContextWithInvocation(agent.ContextWithToolAgentConfig(t.Context(), parent.Cfg), operation.Invocation{CallID: "spawn"})
	if _, err := tool.Execute(ctx, `{"name":"worker","prompt":"task"}`); err != nil {
		t.Fatal(err)
	}
	<-started
	deadline, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	if err := h.Close(deadline); !errors.Is(err, resource.ErrCloseIncomplete) {
		t.Fatalf("close before record finished: %v", err)
	}
	<-recording
	if parent.Cfg.Inbox.Len() != 0 || parent.Cfg.Inbox.ActiveProducers() != 1 {
		t.Fatal("notified or released producer before record")
	}
	unblock()
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	messages := parent.Cfg.Inbox.Drain()
	if len(messages) != 1 || messages[0].Meta["status"] != "canceled" || parent.Cfg.Inbox.ActiveProducers() != 0 {
		t.Fatalf("completion=%v producers=%d", messages, parent.Cfg.Inbox.ActiveProducers())
	}
}

func TestRepeatedLabelsHaveIndependentSessionIDs(t *testing.T) {
	parent := agent.NewAgent(agent.Config{SessionID: "parent", Provider: &scriptedProvider{}, Loop: taskLoop(func(ctx context.Context, _ agent.Config) (*agent.Result, error) { <-ctx.Done(); return nil, ctx.Err() })})
	tool := newSubagentTestTool(t, parent.Cfg)
	ctx := operation.ContextWithInvocation(agent.ContextWithToolAgentConfig(t.Context(), parent.Cfg), operation.Invocation{CallID: "spawn"})
	for range 2 {
		if _, err := tool.Execute(ctx, `{"label":"same","prompt":"task"}`); err != nil {
			t.Fatal(err)
		}
	}
	tool.mu.Lock()
	var ids []string
	for id, run := range tool.runs {
		if run.Detail.AgentName != "same" {
			t.Error("label was renamed")
		}
		ids = append(ids, id)
	}
	tool.mu.Unlock()
	if len(ids) != 2 || ids[0] == ids[1] {
		t.Fatalf("ids=%v", ids)
	}
	if _, err := tool.kill("same"); err == nil {
		t.Fatal("kill accepted an ambiguous label")
	}
	for _, id := range ids {
		if _, err := tool.kill(id); err != nil {
			t.Fatal(err)
		}
	}
	if err := tool.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}
