// Package api defines inert presentation contributions without a terminal runtime.
package api

import (
	"context"
	"fmt"
	"github.com/chainreactors/cyber/core/resource"
	"github.com/spf13/cobra"
	"io"
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

// Registry is a collecting presentation catalog. Registration is allowed only
// before the host publishes Bindings to a running TUI.
type Registry struct {
	mu       sync.Mutex
	batches  []*contributionBatch
	sealed   bool
	closed   bool
	bindings *Bindings
}

type contributionBatch struct {
	items  []*Bindings
	closed bool
}

func NewRegistry() *Registry { return &Registry{} }
func (r *Registry) Add(values ...*Bindings) (resource.Handle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed || r.closed || len(values) == 0 {
		return nil, fmt.Errorf("console registry is not accepting contributions")
	}
	batch := &contributionBatch{items: cloneBindings(values)}
	candidate := r.allLocked()
	candidate = append(candidate, batch.items...)
	b, err := Compose(candidate...)
	if err != nil {
		return nil, err
	}
	r.batches = append(r.batches, batch)
	r.bindings = b
	return resource.HandleFunc(func(context.Context) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		if batch.closed {
			return nil
		}
		batch.closed = true
		for index, current := range r.batches {
			if current == batch {
				r.batches = append(r.batches[:index], r.batches[index+1:]...)
				break
			}
		}
		r.bindings, _ = Compose(r.allLocked()...)
		return nil
	}), nil
}

func (r *Registry) allLocked() []*Bindings {
	var result []*Bindings
	for _, batch := range r.batches {
		if !batch.closed {
			result = append(result, batch.items...)
		}
	}
	return result
}
func (r *Registry) Bindings() *Bindings {
	if r == nil {
		return nil
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
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
	r.closed, r.sealed = true, true
	r.batches, r.bindings = nil, nil
}

// Compose validates a static snapshot without running handlers. Registry uses
// it for atomic registration; hosts may also compose inert bindings directly.
func Compose(bindings ...*Bindings) (*Bindings, error) {
	selected := cloneBindings(bindings)
	owners := map[string]bool{}
	for _, item := range selected {
		if item == nil {
			return nil, fmt.Errorf("console bindings are required")
		}
		if item.Commands == nil {
			continue
		}
		for _, command := range item.Commands(View{Out: io.Discard, Err: io.Discard, Table: func(string, [][]string) {}}) {
			if command == nil || command.Name() == "" {
				return nil, fmt.Errorf("console command requires a name")
			}
			for _, name := range append([]string{command.Name()}, command.Aliases...) {
				if owners[name] {
					return nil, fmt.Errorf("duplicate console command %q", name)
				}
				owners[name] = true
			}
		}
	}
	return &Bindings{
		Commands: func(view View) []*cobra.Command {
			var result []*cobra.Command
			for _, item := range selected {
				if item.Commands != nil {
					result = append(result, item.Commands(view)...)
				}
			}
			return result
		},
		Complete: func(ctx context.Context, value string) []string {
			var result []string
			seen := map[string]bool{}
			for _, item := range selected {
				if item.Complete != nil {
					for _, name := range item.Complete(ctx, value) {
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
				if item.Status != nil {
					result = append(result, item.Status()...)
				}
			}
			return result
		},
	}, nil
}

func cloneBindings(values []*Bindings) []*Bindings {
	result := make([]*Bindings, len(values))
	for index, value := range values {
		if value != nil {
			copy := *value
			result[index] = &copy
		}
	}
	return result
}

var _ resource.Point[*Bindings] = (*Registry)(nil)
