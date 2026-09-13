// Package files assembles the minimal file profile from concrete extensions.
package files

import (
	"context"
	"fmt"
	fileext "github.com/chainreactors/aiscan/pkg/exts/files"
	filesystem "github.com/chainreactors/aiscan/tools/files"
	"sync"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/toolset"
)

const (
	FileSystemID = "files"
)

// Profile is a fixed, preassembled composition. New has no filesystem or
// goroutine side effects. Runtime instance replacement is intentionally absent;
// create a new Profile to apply a different composition.
type Profile struct {
	set      *extension.Set
	registry *toolset.Registry
	mu       sync.RWMutex
	// Public access is enabled only after the entire graph loads.
	active  bool
	closing bool
}

// New constructs the files extension and its host.
func New(config filesystem.Config) (*Profile, error) {
	hookRegistry := hooks.New()
	registry := toolset.NewRegistry(hookRegistry)
	fs, err := fileext.New(registry, hookRegistry, config)
	if err != nil {
		return nil, err
	}
	set, err := extension.New(
		extension.Entry{ID: FileSystemID, Extension: fs},
		extension.Entry{ID: "tool-registry", DependsOn: []string{FileSystemID}, Extension: registry},
	)
	if err != nil {
		return nil, err
	}
	return &Profile{set: set, registry: registry}, nil
}

func (p *Profile) Load(ctx context.Context) error {
	if p == nil {
		return fmt.Errorf("file profile is required")
	}
	p.mu.RLock()
	closing := p.closing
	p.mu.RUnlock()
	if closing {
		return toolset.ErrUnavailable
	}
	if err := p.set.Load(ctx); err != nil {
		return err
	}
	p.mu.Lock()
	if !p.closing {
		p.active = true
	}
	p.mu.Unlock()
	return nil
}

// Executor borrows the host executor after the composition has loaded.
func (p *Profile) Executor() (tool.Executor, error) {
	if p == nil {
		return nil, toolset.ErrUnavailable
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.active || p.closing {
		return nil, toolset.ErrUnavailable
	}
	return p.registry, nil
}

func (p *Profile) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	p.mu.Lock()
	p.closing = true
	p.active = false
	p.mu.Unlock()
	return p.set.Close(ctx)
}

func (p *Profile) Loaded() bool {
	if p == nil {
		return false
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	return p.active && !p.closing
}
