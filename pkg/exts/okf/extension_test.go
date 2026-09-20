package okf

import (
	"context"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	coretool "github.com/chainreactors/cyber/core/tool"
	promptext "github.com/chainreactors/cyber/pkg/exts/prompt"
	skillsext "github.com/chainreactors/cyber/pkg/exts/skills"
)

func TestExtensionPublishesPolicyCommandAndReference(t *testing.T) {
	library, err := skillsext.NewLibrary(skillsext.LibraryConfig{Directory: t.TempDir()})
	if err != nil {
		t.Fatal(err)
	}
	var (
		resolver prompt.Resolver
		executor coretool.CommandExecutor
		store    *skills.Store
	)
	set, err := extension.New(
		extension.Provided[*hooks.Registry](hooks.New()),
		coretool.NewCommandRegistry(),
		library,
		promptext.New(),
		New(),
		extension.Func{LoadFunc: func(scope *extension.Scope) error {
			if resolver, err = extension.Use[prompt.Resolver](scope); err != nil {
				return err
			}
			if executor, err = extension.Use[coretool.CommandExecutor](scope); err != nil {
				return err
			}
			store, err = extension.Use[*skills.Store](scope)
			return err
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = set.Close(context.Background()) })
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}

	main := resolver.Build(t.Context(), prompt.Context{Target: prompt.MainSystem})
	if len(main.Diagnostics) != 0 || !strings.Contains(main.Prompt, "## Open Knowledge Format") {
		t.Fatalf("main prompt = %#v", main)
	}
	evaluator := resolver.Build(t.Context(), prompt.Context{Target: prompt.EvaluatorSystem})
	if strings.Contains(evaluator.Prompt, "## Open Knowledge Format") {
		t.Fatalf("OKF policy leaked into evaluator prompt:\n%s", evaluator.Prompt)
	}
	if !executor.Has("okf") || executor.DescriptionPath("okf") != ReferenceURI {
		t.Fatalf("OKF command was not registered")
	}
	body, handled, err := store.ReadVirtual(ReferenceURI)
	if err != nil || !handled || !strings.Contains(body, "# OKF validation") {
		t.Fatalf("reference handled=%v err=%v body=%q", handled, err, body)
	}
}
