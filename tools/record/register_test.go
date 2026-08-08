//go:build full && (windows || linux)

package record

import (
	"testing"

	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/pkg/commands"
)

func TestRegister(t *testing.T) {
	t.Setenv(maxConcurrentEnv, "3")
	reg := commands.NewRegistry()
	commands.BuildPlan(capability.Select(capability.Options{Groups: []string{"record"}}), &commands.Deps{
		WorkDir: t.TempDir(),
	}, reg)
	tool, ok := reg.GetTool("record")
	if !ok {
		t.Fatal("record tool is not registered")
	}
	if got := tool.(*Tool).maxConcurrent; got != 3 {
		t.Fatalf("max concurrent = %d, want 3", got)
	}
}

func TestRegisterInvalidMaxConcurrentUsesDefault(t *testing.T) {
	t.Setenv(maxConcurrentEnv, "17")
	reg := commands.NewRegistry()
	commands.BuildPlan(capability.Select(capability.Options{Groups: []string{"record"}}), &commands.Deps{
		WorkDir: t.TempDir(),
	}, reg)
	tool, ok := reg.GetTool("record")
	if !ok {
		t.Fatal("record tool is not registered")
	}
	if got := tool.(*Tool).maxConcurrent; got != defaultMaxConcurrent {
		t.Fatalf("max concurrent = %d, want default %d", got, defaultMaxConcurrent)
	}
}
