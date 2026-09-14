// Package api defines inert presentation contributions without a terminal runtime.
package api

import (
	"context"
	"fmt"
	"github.com/spf13/cobra"
	"io"
	"strings"
	"sync"
)

type Row struct{ Name, Value string }
type View struct {
	Out, Err io.Writer
	Table    func(string, [][]string)
	// Session dispatch is supplied by the attached terminal, never captured
	// from one particular session when a feature registers its commands.
	Command       func(string) error
	RefreshStatus func()
}
type Bindings struct {
	Commands func(View) []*cobra.Command
	Complete func(context.Context, string) []string
	Status   func() []Row
}

// Contribution groups a feature's inert presentation factories under its source.
// Commands must return the same names for every View and must not perform I/O.
type Contribution struct {
	Source   string
	Bindings *Bindings
}

// Registrar is the presentation extension point. TUI/REPL owners lend it
// during construction and open it in Load; feature extensions register commands, completion and
// status contributions without depending on the concrete terminal runtime.
type Registrar interface {
	Register(Contribution) error
}

// Registry is a collecting presentation catalog. Registration is allowed only
// before the host publishes Bindings to a running TUI.
type Registry struct {
	mu             sync.Mutex
	items          []Contribution
	active, sealed bool
	bindings       *Bindings
}

func NewRegistry() *Registry { return &Registry{} }
func (r *Registry) Open() error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return fmt.Errorf("console registry is sealed")
	}
	r.active = true
	return nil
}
func (r *Registry) Register(c Contribution) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active || r.sealed {
		return fmt.Errorf("console registry is not accepting contributions")
	}
	for _, item := range r.items {
		if item.Source == c.Source {
			return fmt.Errorf("duplicate console source %q", c.Source)
		}
	}
	if c.Bindings != nil {
		copy := *c.Bindings
		c.Bindings = &copy
	}
	candidate := append(append([]Contribution(nil), r.items...), c)
	// Validate eagerly so a bad/duplicate contribution cannot leak at publish.
	b, err := Compose(candidate...)
	if err != nil {
		return err
	}
	r.items, r.bindings = candidate, b
	return nil
}
func (r *Registry) Bindings() *Bindings {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if !r.active {
		return nil
	}
	r.sealed = true
	if r.bindings == nil {
		return &Bindings{}
	}
	copy := *r.bindings
	return &copy
}
func (r *Registry) Close() {
	r.mu.Lock()
	defer r.mu.Unlock()
	r.active, r.sealed = false, true
	r.items, r.bindings = nil, nil
}

// Compose validates a static snapshot without running handlers. Registry uses
// it for atomic registration; hosts may also compose inert bindings directly.
func Compose(contributions ...Contribution) (*Bindings, error) {
	selected := append([]Contribution(nil), contributions...)
	owners := map[string]string{}
	for index, item := range selected {
		if strings.TrimSpace(item.Source) == "" || item.Bindings == nil {
			return nil, fmt.Errorf("console contribution requires source and bindings")
		}
		copy := *item.Bindings
		item.Bindings = &copy
		selected[index] = item
		if item.Bindings.Commands == nil {
			continue
		}
		for _, command := range item.Bindings.Commands(View{Out: io.Discard, Err: io.Discard, Table: func(string, [][]string) {}}) {
			if command == nil || command.Name() == "" {
				return nil, fmt.Errorf("console command requires a name (%s)", item.Source)
			}
			for _, name := range append([]string{command.Name()}, command.Aliases...) {
				if previous, found := owners[name]; found {
					return nil, fmt.Errorf("duplicate console command %q (sources %s and %s)", name, previous, item.Source)
				}
				owners[name] = item.Source
			}
		}
	}
	return &Bindings{
		Commands: func(view View) []*cobra.Command {
			var result []*cobra.Command
			for _, item := range selected {
				if item.Bindings.Commands != nil {
					result = append(result, item.Bindings.Commands(view)...)
				}
			}
			return result
		},
		Complete: func(ctx context.Context, value string) []string {
			var result []string
			seen := map[string]bool{}
			for _, item := range selected {
				if item.Bindings.Complete != nil {
					for _, name := range item.Bindings.Complete(ctx, value) {
						if !seen[name] {
							seen[name] = true
							result = append(result, name)
						}
					}
				}
			}
			return result
		},
		Status: func() []Row {
			var result []Row
			for _, item := range selected {
				if item.Bindings.Status != nil {
					result = append(result, item.Bindings.Status()...)
				}
			}
			return result
		},
	}, nil
}
