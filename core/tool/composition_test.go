package tool_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	coretool "github.com/chainreactors/cyber/core/tool"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
)

func TestFailedCompositionDiscardsDeclarationsAndClosesOwnedResources(t *testing.T) {
	loadFailure := errors.New("failed after contributing")
	var previousTools *coretool.ToolRegistry
	for _, fail := range []bool{true, false} {
		hookRegistry := hooks.New()
		tools := coretool.NewToolRegistry()
		cmds := coretool.NewCommandRegistry()
		if tools == previousTools {
			t.Fatal("reused a failed registry")
		}
		previousTools = tools
		path := filepath.Join(t.TempDir(), "owned-output")
		var file *os.File
		var subscription *hooks.Subscription
		closes := 0
		owner := extension.Func{
			LoadFunc: func(scope *extension.Scope) error {
				var err error
				file, err = os.Create(path)
				if err != nil {
					return err
				}
				subscription = toolhooks.Started.On(hookRegistry, "mixed", func(context.Context, toolhooks.CallEvent) (struct{}, error) {
					return struct{}{}, nil
				})
				if err := extension.Add[coretool.Tool](scope, echoTool("echo")); err != nil {
					return err
				}
				if err := extension.Add(scope, coretool.Command{
					Name: "echo", Run: func(context.Context, *coretool.Execution) (any, error) { return "ok", nil },
				}); err != nil {
					return err
				}
				if fail {
					return loadFailure
				}
				return nil
			},
			CloseFunc: func(ctx context.Context) error {
				if subscription != nil {
					if err := subscription.Close(ctx); err != nil {
						return err
					}
				}
				closes++
				if file != nil {
					return file.Close()
				}
				return nil
			},
		}
		set, err := extension.New(
			extension.Provided[*hooks.Registry](hookRegistry),
			cmds,
			tools,
			owner,
		)
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() {
			if err := set.Close(context.Background()); err != nil {
				t.Error(err)
			}
		})
		if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
			t.Fatal("construction opened resources")
		}
		err = set.Load(t.Context())
		if fail {
			if !errors.Is(err, loadFailure) {
				t.Fatalf("Load = %v", err)
			}
			if set.Active() || len(tools.ToolDefinitions()) != 0 || len(cmds.Names()) != 0 {
				t.Fatal("failed composition published declarations")
			}
			if err := set.Load(t.Context()); err == nil {
				t.Fatal("reloaded a failed composition")
			}
		} else {
			if err != nil {
				t.Fatal(err)
			}
			if len(tools.ToolDefinitions()) != 1 || len(cmds.Names()) != 1 {
				t.Fatal("fresh composition did not publish its declarations")
			}
			if result, err := cmds.Execute(t.Context(), "echo", &coretool.Execution{}); err != nil || result != "ok" {
				t.Fatalf("command = %v, %v", result, err)
			}
		}
		if err := set.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if closes != 1 || toolhooks.Started.Has(hookRegistry) {
			t.Fatal("owned resources not cleaned exactly once")
		}
		if _, err := file.WriteString("after close"); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("file still usable: %v", err)
		}
	}
}
