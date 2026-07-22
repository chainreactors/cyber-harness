package commands

import "testing"

func closeRegistryTools(registry *CommandRegistry) {
	for _, tool := range registry.Tools() {
		if closer, ok := tool.(interface{ Close() }); ok {
			closer.Close()
		}
	}
}

func TestNativeListToolIsRunnerOnly(t *testing.T) {
	regular := NewRegistry()
	BuildGroup("core", &Deps{WorkDir: t.TempDir()}, regular)
	defer closeRegistryTools(regular)
	if _, ok := regular.GetTool("ls"); ok {
		t.Fatal("regular agent must not expose the runner-only ls tool")
	}

	runner := NewRegistry()
	BuildGroup("core", &Deps{WorkDir: t.TempDir(), RunnerMode: true}, runner)
	defer closeRegistryTools(runner)
	if _, ok := runner.GetTool("ls"); !ok {
		t.Fatal("runner mode must expose the native ls tool")
	}
}
