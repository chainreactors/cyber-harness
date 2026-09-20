package agent

import (
	"context"
	"fmt"
	"testing"
	"time"

	"github.com/chainreactors/cyber/aop"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
	"github.com/chainreactors/cyber/core/telemetry"
	ioaext "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	promptext "github.com/chainreactors/cyber/pkg/exts/prompt"
	ioatools "github.com/chainreactors/cyber/tools/ioa"
	"github.com/chainreactors/ioa/protocols"
)

// Exercise real task creation through the extension, without an HTTP server or LLM.
func TestSubagentWithMemoryIOA(t *testing.T) {
	for _, mode := range []string{"sync", "async", "fork"} {
		t.Run(mode, func(t *testing.T) {
			reg := hooks.New()
			ioa := ioaext.New(ioatools.Config{})
			set, err := extension.New(extension.Provided[*hooks.Registry](reg), promptext.New(), extension.Provided(telemetry.NewLoggerRef(nil)), ioa, ioaext.NewCollaboration(ioaext.CollaborationOptions{}))
			if err != nil {
				t.Fatal(err)
			}
			if err = set.Load(t.Context()); err != nil {
				t.Fatal(err)
			}
			defer func() {
				if err := set.Close(context.Background()); err != nil {
					t.Error(err)
				}
			}()
			parent := NewAgent(Config{SessionID: "parent", Hooks: reg, Provider: &scriptedProvider{}, Messages: []*aop.Message{TextInput("private inherited history")}, Loop: taskLoop(func(ctx context.Context, cfg Config) (*Result, error) {
				records, err := ioa.Service().ReadPublic(ctx, ioa.Service().ReceiveSpace(), protocols.ReadOptions{All: true})
				if err != nil || len(records) != 1 || records[0].Content["message"] != "task" {
					return nil, fmt.Errorf("missing dispatch before execution: %v %v", records, err)
				}
				if records[0].Meta["subagent"].(map[string]any)["session_id"] != cfg.SessionID {
					return nil, fmt.Errorf("wrong child identity")
				}
				return &Result{Output: "done", Stop: StopReasonCompleted}, nil
			})})
			tool := NewSubAgentTool(nil)
			defer tool.Close(context.Background())
			ctx := operation.ContextWithInvocation(withToolAgentConfig(t.Context(), parent.Cfg), operation.Invocation{CallID: "dispatch"})
			if _, err = tool.Execute(ctx, fmt.Sprintf(`{"mode":%q,"prompt":"task"}`, mode)); err != nil {
				t.Fatal(err)
			}
			if mode != "sync" {
				wait, cancel := context.WithTimeout(t.Context(), time.Second)
				defer cancel()
				if !parent.Cfg.Inbox.Wait(wait) {
					t.Fatal("no completion")
				}
			}
			records, err := ioa.Service().ReadPublic(t.Context(), ioa.Service().ReceiveSpace(), protocols.ReadOptions{All: true})
			if err != nil || len(records) != 2 || records[1].Refs.Messages[0] != records[0].ID || records[1].Content["message"] != "done" {
				t.Fatalf("task trace: %v %v", records, err)
			}
			if records[0].Content["input"] != "task" {
				t.Fatal("inherited history copied into handoff")
			}
		})
	}
}
