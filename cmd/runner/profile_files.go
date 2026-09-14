package main

import (
	"context"
	"fmt"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/core/tool"
	fileext "github.com/chainreactors/aiscan/pkg/exts/files"
	profilepkg "github.com/chainreactors/aiscan/pkg/profile"
	"github.com/chainreactors/aiscan/pkg/toolset"
	filesystem "github.com/chainreactors/aiscan/tools/files"
)

const fileSystemID = "files"

// Profile is a fixed, preassembled composition. New has no filesystem or
// goroutine side effects. Runtime instance replacement is intentionally absent;
// create a new Profile to apply a different composition.
type fileProfile struct {
	assembly *profilepkg.Assembly
	registry *toolset.Registry
}

// New constructs the files extension and its host.
func newFileProfile(config filesystem.Config) (*fileProfile, error) {
	hookRegistry := hooks.New()
	registry := toolset.NewRegistry(hookRegistry)
	fs, err := fileext.New(registry, hookRegistry, config)
	if err != nil {
		return nil, err
	}
	assembly, err := profilepkg.Assemble(
		extension.Entry{ID: fileSystemID, Extension: fs},
		extension.Entry{ID: "tool-registry", DependsOn: []string{fileSystemID}, Extension: registry},
	)
	if err != nil {
		return nil, err
	}
	return &fileProfile{assembly: assembly, registry: registry}, nil
}

func (p *fileProfile) Load(ctx context.Context) error {
	if p == nil {
		return fmt.Errorf("file profile is required")
	}
	return p.assembly.Load(ctx)
}

// Executor publishes the host executor after the composition has loaded.
func (p *fileProfile) Executor() (tool.Executor, error) {
	if p == nil {
		return nil, toolset.ErrUnavailable
	}
	if p.assembly == nil || !p.assembly.Available() {
		return nil, toolset.ErrUnavailable
	}
	return p.registry, nil
}

func (p *fileProfile) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	return p.assembly.Close(ctx)
}

func (p *fileProfile) Loaded() bool {
	if p == nil {
		return false
	}
	return p.assembly != nil && p.assembly.Available()
}
