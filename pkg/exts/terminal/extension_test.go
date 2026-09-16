package terminal

import (
	"context"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/pkg/toolset"
)

func TestExtensionOwnsTerminalRegistrationAndShellBinding(t *testing.T) {
	commands := commands.NewRegistry(nil)
	tools := toolset.NewRegistry(nil)
	instance, err := New(nil, commands, Config{Directory: t.TempDir(), Timeout: 5})
	if err != nil {
		t.Fatal(err)
	}
	set, err := extension.New(
		commands,
		tools,
		instance,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !hasTool(tools, "bash") || !commands.Has("tmux") {
		t.Fatal("terminal instance did not publish bash and tmux")
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hasTool(tools, "bash") || commands.Has("tmux") {
		t.Fatal("terminal instance left registrations published after close")
	}
}

func TestExtensionPublishesProfileTmuxAndHidesControlCommands(t *testing.T) {
	commandRegistry := commands.NewRegistry(nil)
	tools := toolset.NewRegistry(nil)
	control := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		return extension.Add(scope, commands.Command{
			Name: "proxy",
			Run:  func(context.Context, *commands.Execution) (any, error) { return "control", nil },
		})
	}}
	custom := commands.Command{
		Name: "tmux",
		Run:  func(context.Context, *commands.Execution) (any, error) { return "profile", nil },
	}
	instance, err := New(nil, commandRegistry, Config{
		Directory:      t.TempDir(),
		Timeout:        5,
		HiddenCommands: []string{"proxy"},
		Tmux:           func(*commands.BashTool) commands.Command { return custom },
	})
	if err != nil {
		t.Fatal(err)
	}
	set, err := extension.New(
		commandRegistry,
		tools,
		control,
		instance,
	)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if got := commandRegistry.Names(); len(got) != 2 || got[0] != "proxy" || got[1] != "tmux" {
		t.Fatalf("published commands = %v", got)
	}
	description := instance.Bash().Description()
	if strings.Contains(description, "proxy") || !strings.Contains(description, "tmux") {
		t.Fatalf("bash description did not apply visibility policy: %q", description)
	}
	result, err := commandRegistry.Execute(t.Context(), "tmux", &commands.Execution{})
	if err != nil || result != "profile" {
		t.Fatalf("profile tmux result = %#v, err=%v", result, err)
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func hasTool(registry tool.Executor, name string) bool {
	for _, definition := range registry.ToolDefinitions() {
		if definition.Name == name {
			return true
		}
	}
	return false
}
