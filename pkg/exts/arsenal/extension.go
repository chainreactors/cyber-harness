// Package arsenal owns package-manager initialization and its command declaration.
package arsenal

import (
	_ "embed"
	"fmt"
	"os"
	"path/filepath"

	crtm "github.com/chainreactors/crtm/pkg"
	"github.com/chainreactors/crtm/pkg/registry"
	"github.com/chainreactors/cyber/core/extension"
	tool "github.com/chainreactors/cyber/tools/arsenal"
)

//go:embed arsenal.yaml
var catalog []byte

type Extension struct {
	directory string
	options   crtm.ManagerOption
}

// New adds the harness catalog to the distribution's definitions and sources.
// The executable owns which tools it ships; Arsenal owns the full catalog.
func New(directory string, options crtm.ManagerOption) (*Extension, error) {
	if !filepath.IsAbs(directory) {
		return nil, fmt.Errorf("arsenal requires an absolute directory")
	}
	entries, err := registry.ParseYAML(catalog)
	if err != nil {
		return nil, err
	}
	options.Tools = registry.Merge(entries, options.Tools)
	return &Extension{directory: directory, options: options}, nil
}
func (e *Extension) BinDir() string { return filepath.Join(e.directory, "bin") }
func (e *Extension) Load(scope *extension.Scope) error {
	if err := scope.Init().Err(); err != nil {
		return err
	}
	options := e.options
	options.BinPath, options.ConfigPath = e.BinDir(), filepath.Join(e.directory, "cyber.yaml")
	mgr, err := crtm.NewManager(options)
	if err != nil {
		return err
	}
	if err := os.MkdirAll(e.BinDir(), 0755); err != nil {
		return err
	}
	for _, source := range options.Sources {
		if bundle, ok := source.(*crtm.Bundle); ok {
			if err := mgr.Prepare(scope.Init(), bundle); err != nil {
				return err
			}
		}
	}
	return extension.Add(scope, tool.NewCommand(mgr))
}
