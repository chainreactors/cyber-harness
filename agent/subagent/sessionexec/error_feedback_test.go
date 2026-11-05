package sessionexec

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/subagent"
	aop "github.com/chainreactors/cyber/aop"
	coretool "github.com/chainreactors/cyber/core/tool"
	types "github.com/chainreactors/cyber/core/types"
)

func TestSubagentFailureAllowsParentModelToContinue(t *testing.T) {
	for _, tc := range []struct {
		name, args string
		panicChild bool
	}{
		{"sync-child-failure", `{"mode":"sync","name":"worker","prompt":"task"}`, false},
		{"sync-child-panic", `{"mode":"sync","name":"worker","prompt":"task"}`, true},
		{"fork-unknown-agent", `{"mode":"fork","name":"missing","prompt":"task"}`, false},
		{"async-invalid-timeout", `{"mode":"async","timeout":"1s","prompt":"task"}`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := agent.NewAgent(agent.Config{SessionID: "parent", Provider: &scriptedProvider{}, Loop: taskLoop(func(context.Context, agent.Config) (*agent.Result, error) {
				if tc.panicChild {
					panic("child panic")
				}
				return nil, errors.New("child failed")
			})}).Cfg
			tool := newSubagentTestTool(t, cfg)
			defer tool.closeRuntime(context.Background())
			childLoop := cfg.Loop
			_, registerErr := tool.registry.Add(subagent.Subagent{Name: "worker", Prepare: func(_ context.Context, child agent.Config, _ subagent.Input) (agent.Config, string, error) {
				child.Loop = childLoop
				return child, "task", nil
			}})
			if registerErr != nil {
				t.Fatal(registerErr)
			}
			turns := 0
			llm := &callbackProvider{fn: func(_ context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
				turns++
				if turns == 1 {
					return chatResponse(&aop.Message{Role: "assistant", Content: []*aop.Content{{Value: &aop.Content_ToolCall{ToolCall: &aop.ToolCall{Id: "spawn", Name: "subagent", Arguments: &aop.EncodedValue{Data: []byte(tc.args), MediaType: aop.JSONMediaType}}}}}}), nil
				}
				last := req.Messages[len(req.Messages)-1]
				r := last.Content[0].GetToolResult()
				if r == nil || !r.IsError || r.Terminate || strings.TrimSpace(coretool.ResultText(r)) == "" {
					return nil, fmt.Errorf("invalid failure feedback: %v", r)
				}
				return chatResponse(newTextMessage("assistant", "parent continued")), nil
			}}
			cfg.Loop = agent.StandardLoop{}
			cfg.Provider = llm
			cfg.Tools = newTestTools(t, tool)
			result, err := agent.NewAgent(cfg).Run(t.Context(), agent.TextInput("delegate task"))
			if err != nil || result == nil || result.Output != "parent continued" || turns != 2 {
				t.Fatalf("turns=%d result=%+v err=%v", turns, result, err)
			}
		})
	}
}

func TestBackgroundCompletionReportsErrorWithoutResult(t *testing.T) {
	for _, mode := range []subagent.Mode{subagent.Async, subagent.Fork} {
		t.Run(string(mode), func(t *testing.T) {
			ib := inbox.NewBuffered(8)
			defer ib.Close()
			tool := &Tool{runs: make(map[string]*subagent.Run)}
			task := &subagent.Run{Mode: mode, Detail: &types.DelegationDetail{AgentName: "worker", Task: "task"}}
			closed := tool.track("child", task, agent.Config{Inbox: ib})
			closed(session.Outcome{Started: true, Err: errors.New("child failed before producing output")})
			closed(session.Outcome{Started: true, Err: errors.New("duplicate")})
			messages := ib.Drain()
			if len(messages) != 1 || !strings.Contains(provider.MessageText(messages[0].Message), `status="failed"`) || !strings.Contains(provider.MessageText(messages[0].Message), "child failed before producing output") || ib.ActiveProducers() != 0 || tool.list() != "No subagents running." {
				t.Fatalf("messages=%+v active=%d", messages, ib.ActiveProducers())
			}
		})
	}
}

func TestBackgroundChildFailureNotifiesParent(t *testing.T) {
	for _, mode := range []string{"async", "fork"} {
		t.Run(mode, func(t *testing.T) {
			parent := agent.NewAgent(agent.Config{SessionID: "parent", Provider: &scriptedProvider{}, Loop: taskLoop(func(context.Context, agent.Config) (*agent.Result, error) {
				return nil, fmt.Errorf("child task failed")
			})})
			tool := newSubagentTestTool(t, parent.Cfg)
			defer tool.closeRuntime(context.Background())
			cfg := parent.Cfg
			// The tool executor supplies invocation identity and the public agent context.
			exec := newTestTools(t, tool)
			ctx := agent.ContextWithToolAgentConfig(t.Context(), cfg)
			r, err := exec.ExecuteTool(ctx, "subagent", fmt.Sprintf(`{"mode":%q,"prompt":"task"}`, mode))
			if err != nil || r.IsError || r.Terminate {
				t.Fatalf("dispatch: result=%+v err=%v", r, err)
			}
			wait, cancel := context.WithTimeout(t.Context(), 3*time.Second)
			defer cancel()
			if !cfg.Inbox.Wait(wait) {
				t.Fatal("failure notification missing")
			}
			messages := cfg.Inbox.Drain()
			found := false
			for _, m := range messages {
				if strings.Contains(provider.MessageText(m.Message), `status="failed"`) && strings.Contains(provider.MessageText(m.Message), "child task failed") {
					found = true
				}
			}
			if !found {
				t.Fatalf("failure not reported: %+v", messages)
			}
			if cfg.Inbox.Closed() {
				t.Fatal("child failure closed parent inbox")
			}
		})
	}
}
