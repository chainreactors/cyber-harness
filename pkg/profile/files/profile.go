// Package files assembles the minimal file profile from concrete extensions.
package files

import (
	"context"
	"fmt"
	"sync"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/extensions/toolgroup"
	filesystem "github.com/chainreactors/aiscan/pkg/files"
	"github.com/chainreactors/aiscan/pkg/toolset/filetools"
	"github.com/chainreactors/aiscan/pkg/toolset/registry"
)

const (
	RegistryID   = "files.tool-registry"
	FileSystemID = "files.fs"
	FileToolsID  = "files.tools"
)

// Profile is a fixed, preassembled composition. New has no filesystem or
// goroutine side effects. Runtime instance replacement is intentionally absent;
// create a new Profile to apply a different composition.
type Profile struct {
	registry *registry.Registry
	set      *extension.Set
	mu       sync.RWMutex
	// Public access is enabled only after the entire graph loads. The registry
	// can be internally active earlier, while its tool registrations are loading.
	active  bool
	closing bool
}

// New passes filesystem policy directly to FS, which validates and owns it.
func New(config filesystem.Config) (*Profile, error) {
	fs, err := filesystem.New(config)
	if err != nil {
		return nil, err
	}
	r := registry.New()
	definitions, err := filetools.Tools(fs)
	if err != nil {
		return nil, err
	}
	tools, err := toolgroup.New(r, definitions...)
	if err != nil {
		return nil, err
	}
	set, err := extension.New(
		extension.Entry{ID: RegistryID, Extension: r},
		extension.Entry{ID: FileSystemID, Extension: fs},
		extension.Entry{ID: FileToolsID, DependsOn: []string{RegistryID, FileSystemID}, Extension: tools},
	)
	if err != nil {
		return nil, err
	}
	return &Profile{registry: r, set: set}, nil
}

func (p *Profile) Load(ctx context.Context) error {
	if p == nil {
		return fmt.Errorf("file profile is required")
	}
	p.mu.RLock()
	closing := p.closing
	p.mu.RUnlock()
	if closing {
		return registry.ErrUnavailable
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

// Executor publishes the real registry only after the whole composition has
// loaded. A caller retaining it across Close still goes through its admission
// checks; no forwarding executor or alternate execution path is needed.
func (p *Profile) Executor() (tool.Executor, error) {
	if p == nil {
		return nil, registry.ErrUnavailable
	}
	p.mu.RLock()
	defer p.mu.RUnlock()
	if !p.active || p.closing {
		return nil, registry.ErrUnavailable
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
