package terminaltools

import (
	"context"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/toolset/registry"
)

func TestExtensionOwnsTerminalRegistrationAndShellBinding(t *testing.T) {
	tools := registry.New()
	if err := tools.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	commands := commands.NewRegistry()
	instance, err := New(tools, commands, Config{Directory: t.TempDir(), Timeout: 5})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	if !hasTool(tools, "bash") || !commands.Has("tmux") {
		t.Fatal("terminal instance did not publish bash and tmux")
	}
	if err := instance.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hasTool(tools, "bash") || commands.Has("tmux") {
		t.Fatal("terminal instance left registrations published after close")
	}
	if err := commands.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := tools.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestExtensionPublishesProfileTmuxAndHidesControlCommands(t *testing.T) {
	tools := registry.New()
	if err := tools.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	commandRegistry := commands.NewRegistry()
	if err := commandRegistry.Register("control", "control", commands.Command{
		Name: "proxy",
		Run:  func(context.Context, *commands.Execution) (any, error) { return "control", nil },
	}); err != nil {
		t.Fatal(err)
	}
	custom := commands.Command{
		Name: "tmux",
		Run:  func(context.Context, *commands.Execution) (any, error) { return "profile", nil },
	}
	instance, err := New(tools, commandRegistry, Config{
		Directory:      t.TempDir(),
		Timeout:        5,
		HiddenCommands: []string{"proxy"},
		Tmux:           func(*commands.BashTool) commands.Command { return custom },
	})
	if err != nil {
		t.Fatal(err)
	}
	if err := instance.Load(t.Context()); err != nil {
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
	if err := instance.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := commandRegistry.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	_ = tools.Close(context.Background())
}

func hasTool(registry *registry.Registry, name string) bool {
	for _, definition := range registry.ToolDefinitions() {
		if definition.Name == name {
			return true
		}
	}
	return false
}
