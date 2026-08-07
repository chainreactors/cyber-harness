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

func TestCommandBridgeFactoryIsOptIn(t *testing.T) {
	disabled := NewRegistry()
	BuildPlan(capability.Select(capability.Options{Groups: []string{"core"}}), &Deps{WorkDir: t.TempDir()}, disabled)
	defer closeRegistryTools(disabled)
	disabledTool, ok := disabled.GetTool("bash")
	if !ok || disabledTool.(*BashTool).CommandBridgeEnabled() {
		t.Fatal("regular factory must leave the command bridge disabled")
	}

	enabled := NewRegistry()
	BuildPlan(capability.Select(capability.Options{Groups: []string{"core"}}), &Deps{WorkDir: t.TempDir(), CommandBridge: true}, enabled)
	defer closeRegistryTools(enabled)
	enabledTool, ok := enabled.GetTool("bash")
	if !ok {
		t.Fatal("enabled factory did not register bash")
	}
	bash := enabledTool.(*BashTool)
	if !bash.CommandBridgeEnabled() || bash.CommandBridgeError() != nil {
		t.Fatalf("command bridge enabled=%v error=%v", bash.CommandBridgeEnabled(), bash.CommandBridgeError())
	}
}
