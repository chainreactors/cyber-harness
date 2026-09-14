// Package settings provides the static flag/configuration contribution phase
// of a profile. It reuses config.Sections and cli.Registry, not a second parser.
package settings

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/pkg/cli"
)

// Declaration belongs to a capability extension, but does not require a
// runtime instance. Each profile build supplies fresh option structs. DeclareCLI
// may register inert actions; it must not execute them or acquire resources.
type Declaration = cli.Declaration
type Flag = cli.Flag

// Extension owns one profile's selected declarations. Multiple layers are
// flattened in order, with duplicate owners rejected (never silently replaced).
// A runtime Set is optional for CLI-only invocations such as --help.
type Extension struct {
	mu           sync.Mutex
	declarations []Declaration
	sections     *config.Sections
	declared     bool
	loaded       bool
	closed       bool
}

func New(layers ...[]Declaration) (*Extension, error) {
	e := &Extension{sections: config.NewSections()}
	seen := map[string]bool{}
	for _, layer := range layers {
		for _, d := range layer {
			if strings.TrimSpace(d.ID) == "" {
				return nil, fmt.Errorf("settings declaration requires extension ID")
			}
			if seen[d.ID] {
				return nil, fmt.Errorf("duplicate settings extension %q", d.ID)
			}
			seen[d.ID] = true
			if err := e.sections.Register(d.ID, d.Config...); err != nil {
				return nil, err
			}
			d.Config = nil // Sections retains the validated configuration declarations.
			d.Flags = append([]Flag(nil), d.Flags...)
			e.declarations = append(e.declarations, d)
		}
	}
	e.sections.Seal()
	return e, nil
}

// Sections returns a sealed declaration catalog usable before runtime loading.
func (e *Extension) Sections() *config.Sections { return e.sections }

// Declare installs the selected CLI surface exactly once, before Parse/Load.
// On failure discard the parser and this installation; actions never run here.
// The host seals the CLI after all declarations (including host flags) are in.
func (e *Extension) Declare(registry *cli.Registry) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if registry == nil {
		return fmt.Errorf("settings requires CLI registry")
	}
	if e.declared || e.loaded || e.closed {
		return fmt.Errorf("settings declarations are sealed")
	}
	e.declared = true
	// Commands must exist before any extension contributes flags to them.
	for _, d := range e.declarations {
		if d.DeclareCLI != nil {
			if err := d.DeclareCLI(registry); err != nil {
				return fmt.Errorf("declare extension %s: %w", d.ID, err)
			}
		}
	}
	for _, d := range e.declarations {
		for _, flag := range d.Flags {
			if err := registry.Group(d.ID, flag.Command, flag.Key, flag.Group); err != nil {
				return fmt.Errorf("declare extension %s: %w", d.ID, err)
			}
		}
	}
	return nil
}

func (e *Extension) Load(scope *extension.Scope) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed || scope == nil {
		return fmt.Errorf("settings is unavailable")
	}
	if err := scope.Init().Err(); err != nil {
		return err
	}
	e.loaded = true
	return nil
}

func (e *Extension) Close(context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.closed = true
	return nil
}

var _ extension.Extension = (*Extension)(nil)
