package commands

import (
	"testing"

	"github.com/chainreactors/aiscan/core/capability"
)

func closeRegistryTools(registry *CommandRegistry) {
	for _, tool := range registry.Tools() {
		if closer, ok := tool.(interface{ Close() }); ok {
			closer.Close()
		}
	}
}

func TestNativeListToolIsRunnerOnly(t *testing.T) {
	regular := NewRegistry()
	BuildPlan(capability.Select(capability.Options{Groups: []string{"core"}}), &Deps{WorkDir: t.TempDir()}, regular)
	defer closeRegistryTools(regular)
	if _, ok := regular.GetTool("ls"); ok {
		t.Fatal("regular agent must not expose the runner-only ls tool")
	}

	runner := NewRegistry()
	BuildPlan(capability.Select(capability.Options{Groups: []string{"core"}}), &Deps{WorkDir: t.TempDir(), RunnerMode: true}, runner)
	defer closeRegistryTools(runner)
	if _, ok := runner.GetTool("ls"); !ok {
		t.Fatal("runner mode must expose the native ls tool")
	}
}

func TestCoreFactoryAttachesRegisteredCommandsToShell(t *testing.T) {
	registry := NewRegistry()
	BuildPlan(capability.Select(capability.Options{Groups: []string{"core"}}), &Deps{WorkDir: t.TempDir()}, registry)
	defer closeRegistryTools(registry)
	tool, ok := registry.GetTool("bash")
	if !ok {
		t.Fatal("core factory did not register bash")
	}
	bash := tool.(*BashTool)
	if bash.shellRegistry != registry {
		t.Fatal("registered commands were not attached to bash")
	}
	if bash.shellAdapter != nil {
		t.Fatal("shell adapter must remain lazy until a real shell is needed")
	}
}
