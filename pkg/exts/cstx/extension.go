//go:build full && cgo

package cstx

import (
	"context"
	"fmt"
	"sort"
	"sync"

	"github.com/chainreactors/cyber/core/extension"
	libcstx "github.com/chainreactors/libcstx/go"
	"github.com/chainreactors/libcstx/go/proto/cstxproto"
)

// Extension keeps the optional native CSTX runtime available without placing
// it in any product profile. Browser artifact normalization uses the WASM ABI.
type Extension struct {
	mu        sync.Mutex
	runtime   *libcstx.CSTX
	artifacts []string
	closed    bool
}

var _ extension.Extension = (*Extension)(nil)

func New() *Extension { return new(Extension) }

// Load enables every linked extension that advertises parser capability. The
// ABI catalog remains the only source of extension and artifact names.
func (e *Extension) Load(scope *extension.Scope) error {
	ctx := scope.Init()
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return fmt.Errorf("cstx extension is closed")
	}
	if e.runtime != nil {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	runtime, err := libcstx.Open(ctx, &cstxproto.RuntimeConfig{})
	if err != nil {
		return fmt.Errorf("open CSTX runtime: %w", err)
	}
	catalog, err := runtime.Extensions.List(ctx)
	if err != nil {
		_ = runtime.Close()
		return fmt.Errorf("list CSTX extensions: %w", err)
	}
	artifacts := make(map[string]struct{})
	for _, info := range catalog.GetExtensions() {
		if info == nil || info.GetName() == "" || len(info.GetArtifacts()) == 0 {
			continue
		}
		if !info.GetEnabled() {
			if err := runtime.Extensions.Enable(ctx, info.GetName()); err != nil {
				_ = runtime.Close()
				return fmt.Errorf("enable CSTX extension %q: %w", info.GetName(), err)
			}
		}
		for _, artifact := range info.GetArtifacts() {
			artifacts[artifact] = struct{}{}
		}
	}
	e.runtime = runtime
	e.artifacts = make([]string, 0, len(artifacts))
	for artifact := range artifacts {
		e.artifacts = append(e.artifacts, artifact)
	}
	sort.Strings(e.artifacts)
	return nil
}

func (e *Extension) ArtifactTypes() []string {
	e.mu.Lock()
	defer e.mu.Unlock()
	return append([]string(nil), e.artifacts...)
}

func (e *Extension) Close(context.Context) error {
	e.mu.Lock()
	if e.closed {
		e.mu.Unlock()
		return nil
	}
	runtime := e.runtime
	e.runtime = nil
	e.artifacts = nil
	e.closed = true
	e.mu.Unlock()
	if runtime != nil {
		return runtime.Close()
	}
	return nil
}
