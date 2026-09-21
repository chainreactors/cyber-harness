package terminal

import (
	"context"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"

	terminaltool "github.com/chainreactors/cyber/tools/terminal"
)

func TestExtensionOwnsTerminalRegistrationAndShellBinding(t *testing.T) {
	commands := coretool.NewCommandRegistry()
	tools := coretool.NewToolRegistry()
	instance := New(Config{Directory: t.TempDir(), Timeout: 5})
	set, err := extension.New(
		hosttest.Capabilities(),
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
	if !hasTool(tools, "bash") {
		t.Fatal("terminal instance did not publish bash")
	}
	if commands.Has("tmux") {
		t.Fatal("terminal published tmux; that belongs to the tmux extension")
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if hasTool(tools, "bash") {
		t.Fatal("terminal instance left registrations published after close")
	}
}

func TestExtensionHidesControlCommandsAndLetsAHostOwnTmux(t *testing.T) {
	commandRegistry := coretool.NewCommandRegistry()
	tools := coretool.NewToolRegistry()
	control := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		return extension.Add(scope, coretool.Command{
			Name: "proxy",
			Run:  func(context.Context, *coretool.Execution) (any, error) { return "control", nil },
		})
	}}
	// tmux is an ordinary contributed command now, so a host that wants its own
	// session policy contributes one instead of handing this extension a
	// callback. Nothing here has to know the name is special.
	custom := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		return extension.Add(scope, coretool.Command{
			Name: "tmux",
			Run:  func(context.Context, *coretool.Execution) (any, error) { return "profile", nil },
		})
	}}
	instance := New(Config{
		Directory:      t.TempDir(),
		Timeout:        5,
		HiddenCommands: []string{"proxy"},
	})
	var bash *terminaltool.BashTool
	borrow := extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		bash, err = extension.Use[*terminaltool.BashTool](scope)
		return err
	}}
	set, err := extension.New(
		hosttest.Capabilities(),
		commandRegistry,
		tools,
		control,
		instance,
		custom,
		borrow,
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
	description := bash.Description()
	if strings.Contains(description, "proxy") || !strings.Contains(description, "tmux") {
		t.Fatalf("bash description did not apply visibility policy: %q", description)
	}
	result, err := commandRegistry.Execute(t.Context(), "tmux", &coretool.Execution{})
	if err != nil || result != "profile" {
		t.Fatalf("profile tmux result = %#v, err=%v", result, err)
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func hasTool(registry coretool.Executor, name string) bool {
	for _, definition := range registry.ToolDefinitions() {
		if definition.Name == name {
			return true
		}
	}
	return false
}
