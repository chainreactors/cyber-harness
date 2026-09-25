package scanner

import (
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/subagent"
	aop "github.com/chainreactors/cyber/aop"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/operation"
	"github.com/chainreactors/cyber/core/types"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
	subagentext "github.com/chainreactors/cyber/pkg/exts/subagent"
	"github.com/chainreactors/utils/parsers"
)

func installWorkers(t *testing.T, read func(string) string) subagent.Executor {
	t.Helper()
	var executor subagent.Executor
	hosttest.Load(t, t.Context(), subagentext.New(), extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		executor, err = extension.Use[subagent.Executor](scope)
		if err != nil {
			return err
		}
		return extension.Add(scope, scannerSubagents(read)...)
	}})
	return executor
}

func TestScannerNamedWorkersSharePreparation(t *testing.T) {
	for _, name := range []string{"verify", "sniper"} {
		t.Run(name, func(t *testing.T) {
			llm := &workerProvider{}
			resolver := &workerPromptResolver{}
			bus := coreevents.New()
			var events []*aop.Event
			bus.Observe(func(ev *aop.Event) { events = append(events, ev) })
			cfg := agent.Config{Loop: agent.StandardLoop{}, Provider: llm, Model: "scan-model", PromptResolver: resolver, Bus: bus}
			reads := 0
			executor := installWorkers(t, func(skill string) string {
				reads++
				if skill != name {
					t.Errorf("skill=%s", skill)
				}
				return "instructions"
			})
			ctx := operation.ContextWithInvocation(t.Context(), operation.Invocation{SessionID: "caller", CallID: "scan-call"})
			result, err := executor.Execute(ctx, cfg, subagent.Request{Name: name, Input: subagent.Input{Payload: parsers.Loot{Target: "http://example.test", Description: "finding"}}})
			if err != nil || result == nil || result.Output != "status:confirmed" {
				t.Fatalf("result=%v error=%v", result, err)
			}
			if reads != 1 || len(resolver.inputs) != 2 {
				t.Fatalf("reads=%d prompts=%v", reads, resolver.inputs)
			}
			for _, input := range resolver.inputs {
				if input.Agent.Name != name || input.Agent.Model != "scan-model" {
					t.Fatalf("context=%v", input.Agent)
				}
			}
			start := events[0]
			detail, ok, err := types.GetDelegation(start)
			if err != nil || !ok || detail.AgentType != name || detail.RunMode != types.DelegationRunForeground {
				t.Fatalf("delegation=%v err=%v", detail, err)
			}
			if start.GetSessionStarted().GetParentSessionId() != "caller" || start.GetSessionStarted().GetParentToolCallId() != "scan-call" {
				t.Fatalf("start=%v", start)
			}
			if events[len(events)-1].GetSessionEnded() == nil {
				t.Fatal("worker did not finish")
			}
			result, err = executor.Execute(ctx, cfg, subagent.Request{Name: name, Input: subagent.Input{Prompt: "text task"}})
			if err != nil || result.Output != "status:confirmed" || reads != 2 {
				t.Fatalf("text result=%v reads=%d err=%v", result, reads, err)
			}
		})
	}
}

func TestScannerPreparationRejectsInvalidInput(t *testing.T) {
	cfg := agent.Config{PromptResolver: &workerPromptResolver{}}
	worker := scannerSubagents(func(string) string { return "instructions" })[0]
	for _, input := range []subagent.Input{{}, {Prompt: "text", Payload: "bad"}, {Payload: parsers.Loot{}}} {
		if _, _, err := worker.Prepare(t.Context(), cfg, input); err == nil {
			t.Fatalf("accepted %#v", input)
		}
	}
	_, _, err := scannerSubagents(func(string) string { return "" })[0].Prepare(t.Context(), cfg, subagent.Input{Prompt: "text"})
	if err == nil || !strings.Contains(err.Error(), "skill is unavailable") {
		t.Fatalf("missing skill: %v", err)
	}
}

func TestScannerExtensionContributesNamedSubagentsWithoutStartupModel(t *testing.T) {
	var executor subagent.Executor
	installed := installScanner(t, t.TempDir(), Config{}, extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		executor, err = extension.Use[subagent.Executor](scope)
		return err
	}})
	names := map[string]bool{}
	for _, value := range executor.Catalog() {
		names[value.Name] = true
	}
	if !names["verify"] || !names["sniper"] || len(names) != 2 {
		t.Fatalf("registered=%v", names)
	}
	llm := &workerProvider{}
	cfg := agent.Config{Loop: agent.StandardLoop{}, Provider: llm, Model: "caller-model", PromptResolver: installed.prompts}
	for _, name := range []string{"verify", "sniper"} {
		result, err := executor.Execute(t.Context(), cfg, subagent.Request{Name: name, Input: subagent.Input{Prompt: "Analyze http://example.test"}})
		if err != nil || result.Output != "status:confirmed" {
			t.Fatalf("%s: result=%v err=%v", name, result, err)
		}
	}
}
