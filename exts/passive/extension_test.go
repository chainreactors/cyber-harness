package passive

import (
	"context"
	"testing"

	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/pkg/harness"
	"github.com/chainreactors/cyber/pkg/testutil/hosttest"
	"github.com/chainreactors/cyber/tools/files"
	"github.com/chainreactors/cyber/tools/passive"
)

func TestIndependentCommandAndDocsLifecycle(t *testing.T) {
	entries, err := harness.BaseExtensions(harness.BaseConfig{Directory: t.TempDir(), Provider: provider.StartupConfig{Mode: provider.StartupDisabled}})
	if err != nil {
		t.Fatal(err)
	}
	entries = append(entries, New(passive.Config{}))
	var commands coretool.CommandExecutor
	var fs *files.Files
	entries = append(entries, extension.Func{LoadFunc: func(scope *extension.Scope) error {
		var err error
		commands, err = extension.Use[coretool.CommandExecutor](scope)
		if err != nil {
			return err
		}
		fs, err = extension.Use[*files.Files](scope)
		return err
	}})
	set := hosttest.Load(t, t.Context(), entries...)
	if !commands.Has("passive") || commands.Has("scan") {
		t.Fatalf("commands=%v", commands.Names())
	}
	body, err := fs.Read(t.Context(), commands.DescriptionPath("passive"))
	if err != nil || len(body) == 0 {
		t.Fatalf("docs: %v", err)
	}
	if err = set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if commands.Has("passive") {
		t.Fatal("command survived extension close")
	}
}
