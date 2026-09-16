package app

import (
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/pkg/toolset"
)

func TestNewRequiresProfileDependencies(t *testing.T) {
	for _, missing := range []string{"hooks", "events", "command registry", "tool registry"} {
		t.Run(missing, func(t *testing.T) {
			hookRegistry := hooks.New()
			deps := Dependencies{
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
			resource, err := New(nil, deps)
			if resource != nil || err == nil || !strings.Contains(err.Error(), missing) {
				t.Fatalf("New without %s = %v, %v", missing, resource, err)
			}
		})
	}
}
