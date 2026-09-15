package main

import (
	"context"
	"fmt"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/tool"
	fileext "github.com/chainreactors/cyber/pkg/exts/files"
	harnessext "github.com/chainreactors/cyber/pkg/exts/harness"
	signalsext "github.com/chainreactors/cyber/pkg/exts/signals"
	"github.com/chainreactors/cyber/pkg/toolset"
	filesystem "github.com/chainreactors/cyber/tools/files"
)

const fileSystemID = "files"

// Profile is a fixed, preassembled composition. New has no filesystem or
// goroutine side effects. Runtime instance replacement is intentionally absent;
// create a new Profile to apply a different composition.
type fileProfile struct {
	extensions *extension.Set
	registry   tool.Executor
}

// New constructs the files extension and its host.
func newFileProfile(config filesystem.Config) (*fileProfile, error) {
	signals := signalsext.New()
	hookRegistry := signals.Hooks()
	harness, err := harnessext.New(hookRegistry)
	if err != nil {
		return nil, err
	}
	registry := harness.ToolRegistry()
	fs, err := fileext.New(registry, hookRegistry, config)
	if err != nil {
		return nil, err
	}
	extensions, err := extension.New(
		extension.Entry{ID: "signals", Extension: signals},
		extension.Entry{ID: fileSystemID, Extension: fs},
		extension.Entry{ID: "harness", DependsOn: []string{fileSystemID}, Extension: harness},
	)
	if err != nil {
		return nil, err
	}
	return &fileProfile{extensions: extensions, registry: registry}, nil
}

func (p *fileProfile) Load(ctx context.Context) error {
	if p == nil {
		return fmt.Errorf("file profile is required")
	}
	return p.extensions.Load(ctx)
}

// Executor publishes the host executor after the composition has loaded.
func (p *fileProfile) Executor() (tool.Executor, error) {
	if p == nil {
		return nil, toolset.ErrUnavailable
	}
	if p.extensions == nil || !p.extensions.Active() {
		return nil, toolset.ErrUnavailable
	}
	return p.registry, nil
}

func (p *fileProfile) Close(ctx context.Context) error {
	if p == nil {
		return nil
	}
	return p.extensions.Close(ctx)
}

func (p *fileProfile) Loaded() bool {
	if p == nil {
		return false
	}
	return p.extensions != nil && p.extensions.Active()
}
