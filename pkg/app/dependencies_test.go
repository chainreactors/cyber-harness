package app

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/core/events"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/internal/extensiontest"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/toolset"
)

func TestNewRequiresProfileDependencies(t *testing.T) {
	for _, missing := range []string{"hooks", "events", "command registry", "tool registry"} {
		t.Run(missing, func(t *testing.T) {
			hookRegistry := hooks.New()
			deps := AppServices{
				Hooks: hookRegistry, Events: events.New(),
				Commands: commands.NewRegistry(hookRegistry), Tools: toolset.NewRegistry(hookRegistry),
			}
			switch missing {
			case "hooks":
				deps.Hooks = nil
			case "events":
				deps.Events = nil
			case "command registry":
				deps.Commands = nil
			case "tool registry":
				deps.Tools = nil
			}
			resource, err := New(Config{}, deps)
			if resource != nil || err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("New without %s = %v, %v", missing, resource, err)
			}
		})
	}
}

func TestAppCloseDoesNotCloseBorrowedRegistries(t *testing.T) {
	hookRegistry := hooks.New()
	cmds, tools := commands.NewRegistry(hookRegistry), toolset.NewRegistry(hookRegistry)
	if err := cmds.Register("test", "test", commands.Command{
		Name: "ping", Run: func(context.Context, *commands.Execution) (any, error) { return "pong", nil },
	}); err != nil {
		t.Fatal(err)
	}
	resource, err := New(Config{SkipEngines: true}, AppServices{
		Hooks: hookRegistry, Events: events.New(), Commands: cmds, Tools: tools,
	})
	if err != nil {
		t.Fatal(err)
	}
	set := extensiontest.Set(t,
		extension.Entry{ID: "app", Extension: resource},
		extension.Entry{ID: "commands", DependsOn: []string{"app"}, Extension: cmds},
		extension.Entry{ID: "tools", DependsOn: []string{"app"}, Extension: tools},
	)
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	// Isolate the App boundary: closing it must not dispose borrowed objects.
	if err := resource.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if result, err := cmds.Execute(t.Context(), "ping", &commands.Execution{}); err != nil || result != "pong" {
		t.Fatalf("borrowed command registry = %v, %v", result, err)
	}
	if _, err := tools.ExecuteTool(t.Context(), "missing", "{}"); !errors.Is(err, toolset.ErrUnknown) {
		t.Fatalf("borrowed tool registry lost admission: %v", err)
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := tools.ExecuteTool(t.Context(), "missing", "{}"); !errors.Is(err, toolset.ErrUnavailable) {
		t.Fatalf("owning Set did not close registry: %v", err)
	}
}
