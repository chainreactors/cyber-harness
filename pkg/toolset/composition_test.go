package toolset_test

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/hooks"
	toolhooks "github.com/chainreactors/aiscan/core/tool/hooks"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/toolset"
)

func TestFailedCompositionDiscardsDeclarationsAndClosesOwnedResources(t *testing.T) {
	loadFailure := errors.New("failed after contributing")
	var previousTools *toolset.Registry
	for _, fail := range []bool{true, false} {
		hookRegistry := hooks.New()
		tools := toolset.NewRegistry(hookRegistry)
		cmds := commands.NewRegistry(hookRegistry)
		if tools == previousTools {
			t.Fatal("reused a failed registry")
		}
		previousTools = tools
		path := filepath.Join(t.TempDir(), "owned-output")
		var file *os.File
		var subscription *hooks.Subscription
		closes := 0
		owner := extension.Func{
			LoadFunc: func(*extension.Scope) error {
				var err error
				file, err = os.Create(path)
				if err != nil {
					return err
				}
				subscription = toolhooks.Started.On(hookRegistry, "mixed", func(context.Context, toolhooks.CallEvent) (struct{}, error) {
					return struct{}{}, nil
				})
				if err := tools.Register("test", echoTool("echo")); err != nil {
					return err
				}
				if err := cmds.Register("test", "mixed", commands.Command{
					Name: "echo", Run: func(context.Context, *commands.Execution) (any, error) { return "ok", nil },
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
			extension.Entry{ID: "mixed", Extension: owner},
			extension.Entry{ID: "commands", DependsOn: []string{"mixed"}, Extension: cmds},
			extension.Entry{ID: "tools", DependsOn: []string{"mixed", "commands"}, Extension: tools},
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
			if result, err := cmds.Execute(t.Context(), "echo", &commands.Execution{}); err != nil || result != "ok" {
				t.Fatalf("command = %v, %v", result, err)
			}
		}
		if err := set.Close(t.Context()); err != nil {
			t.Fatal(err)
		}
		if closes != 1 || hookRegistry.Has(toolhooks.Started.Kind) {
			t.Fatal("owned resources not cleaned exactly once")
		}
		if _, err := file.WriteString("after close"); !errors.Is(err, os.ErrClosed) {
			t.Fatalf("file still usable: %v", err)
		}
	}
}
