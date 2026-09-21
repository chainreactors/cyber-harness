package scanner

import (
	"context"
	"github.com/chainreactors/cyber/core/egress"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	"testing"
)

func TestExecutionNeedsNoAgentModelPromptSkillsOrBash(t *testing.T) {
	commands := coretool.NewCommandRegistry()
	set, err := extension.New(extension.Provided[*hooks.Registry](hooks.New()), extension.Provided[*events.Stream](events.New()), extension.Provided[telemetry.Logger](telemetry.NopLogger()), extension.Provided[egress.Endpoint](egress.Disabled()), commands, NewExecution(Config{}, t.TempDir()))
	if err != nil {
		t.Fatal(err)
	}
	if err = set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	if !commands.Has("curl") {
		t.Fatal("execution commands not installed")
	}
}
func TestAvailabilityDoesNotTurnStaticCatalogIntoReadyCommands(t *testing.T) {
	status := availability([]coretool.Command{{Name: "curl"}, {Name: "proton"}, {Name: "cyberhub"}}, nil)
	for _, item := range status.Commands {
		if item.Name == "curl" {
			if item.State != "ready" {
				t.Fatal(item)
			}
		} else if item.State == "ready" {
			t.Fatalf("missing resources marked ready: %v", item)
		}
	}
}
