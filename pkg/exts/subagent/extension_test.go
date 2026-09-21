package subagent_test

import (
	"context"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/subagent"
	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/internal/testutil/apptest"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	promptext "github.com/chainreactors/cyber/pkg/exts/prompt"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	subagentext "github.com/chainreactors/cyber/pkg/exts/subagent"
)

func TestSessionWithoutSubagentDoesNotInstallTool(t *testing.T) {
	fixture := apptest.NewFixture(t, nil, nil)
	sessions := sessionext.New(session.Config{})
	values := append(apptest.Entries(t, fixture), promptext.New(), loopext.New(agent.NoLoop()), sessions)
	hosttest.Load(t, t.Context(), values...)
	for _, tool := range fixture.Tools.ToolDefinitions() {
		if tool.Name == "subagent" {
			t.Fatal("session installed an optional tool")
		}
	}
	if _, err := sessions.Runtime().OpenSession(t.Context(), session.SessionOptions{ID: "ordinary"}); err != nil {
		t.Fatal(err)
	}
}

func TestToolsBorrowTheSinglePoint(t *testing.T) {
	fixture := apptest.NewFixture(t, nil, nil)
	sessions := sessionext.New(session.Config{})
	definitions := subagentext.New()
	tools := subagentext.NewTools()
	var registry *subagent.Registry
	values := append(apptest.Entries(t, fixture), promptext.New(), loopext.New(agent.NoLoop()), definitions,
		extension.Func{LoadFunc: func(scope *extension.Scope) error {
			var err error
			registry, err = extension.Use[*subagent.Registry](scope)
			if err != nil {
				return err
			}
			return extension.Add(scope, subagent.Subagent{Name: "registered", Description: "from the Point", Prepare: func(_ context.Context, cfg agent.Config, input subagent.Input) (agent.Config, string, error) {
				return cfg, input.Prompt, nil
			}})
		}}, sessions, tools)
	hosttest.Load(t, t.Context(), values...)
	catalog, err := fixture.Tools.ExecuteTool(t.Context(), "subagent", `{"action":"catalog"}`)
	if err != nil || !strings.Contains(coretool.ResultText(catalog), "from the Point") {
		t.Fatalf("catalog=%v err=%v", catalog, err)
	}
	h, err := registry.Add(subagent.Subagent{Name: "dynamic", Description: "after Load", Prepare: func(_ context.Context, cfg agent.Config, input subagent.Input) (agent.Config, string, error) {
		return cfg, input.Prompt, nil
	}})
	if err != nil {
		t.Fatal(err)
	}
	catalog, err = fixture.Tools.ExecuteTool(t.Context(), "subagent", `{"action":"catalog"}`)
	if err != nil || !strings.Contains(coretool.ResultText(catalog), "after Load") {
		t.Fatalf("catalog=%v err=%v", catalog, err)
	}
	if err := h.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	catalog, err = fixture.Tools.ExecuteTool(t.Context(), "subagent", `{"action":"catalog"}`)
	if err != nil || strings.Contains(coretool.ResultText(catalog), "after Load") {
		t.Fatalf("stale catalog=%v err=%v", catalog, err)
	}
}

func TestPointDoesNotRequireSession(t *testing.T) {
	var executor subagent.Executor
	hosttest.Load(t, t.Context(), subagentext.New(), extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		executor, err = extension.Use[subagent.Executor](scope)
		return err
	}})
	run, err := executor.Start(t.Context(), agent.Config{}, subagent.Request{Input: subagent.Input{Prompt: "task"}})
	if err != nil {
		t.Fatal(err)
	}
	run.Finish()
}
