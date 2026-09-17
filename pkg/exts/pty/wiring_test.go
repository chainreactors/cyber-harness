package pty_test

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/namespaces"
	"github.com/chainreactors/cyber/core/resource"
	"github.com/chainreactors/cyber/pkg/commands"
	ptyext "github.com/chainreactors/cyber/pkg/exts/pty"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"
	"github.com/chainreactors/cyber/pkg/hosttest"
	"github.com/chainreactors/cyber/pkg/toolset"
)

// terminal returns the registry that owns the command point and the terminal
// extension that contributes to it and provides the session registry.
func terminal(t *testing.T) ([]extension.Extension, *terminalext.Extension) {
	t.Helper()
	shared := hooks.New()
	commandPoint := commands.NewRegistry(shared)
	toolPoint := toolset.NewRegistry(shared)
	value := terminalext.New(terminalext.Config{Directory: t.TempDir(), Timeout: 1})
	return []extension.Extension{
		namespaces.New(), hosttest.Capabilities(), commandPoint, toolPoint,
	}, value
}

// The PTY protocol borrows the session registry the terminal owns. Ordering it
// first used to hand every connection a nil manager that only failed in use;
// it now fails at load, naming the capability and the extension.
func TestPTYBeforeItsProviderFailsAtLoad(t *testing.T) {
	points, owner := terminal(t)
	set, err := extension.New(append(points, ptyext.New(), owner)...)
	if err != nil {
		t.Fatal(err)
	}
	err = set.Load(context.Background())
	if err == nil {
		t.Fatal("expected the load to fail")
	}
	if !errors.Is(err, resource.ErrTypeUnknown) {
		t.Fatalf("err = %v, want ErrTypeUnknown", err)
	}
	for _, want := range []string{"Sessions", "extension 4"} {
		if !strings.Contains(err.Error(), want) {
			t.Errorf("err = %q, want it to name %q", err, want)
		}
	}
}

func TestPTYAfterItsProviderLoads(t *testing.T) {
	points, owner := terminal(t)
	set, err := extension.New(append(points, owner, ptyext.New())...)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}
