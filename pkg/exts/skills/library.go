package skills

import (
	"context"
	"fmt"
	"github.com/chainreactors/cyber/core/capability"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/skills"
	"path/filepath"
)

type LibraryConfig struct {
	Directory string
	Paths     []string
	Catalog   capability.Catalog
	Bundles   []skills.Bundle
}
type Library struct {
	config LibraryConfig
	store  *skills.Store
}

func NewLibrary(config LibraryConfig) (*Library, error) {
	if !filepath.IsAbs(config.Directory) {
		return nil, fmt.Errorf("skills directory must be absolute")
	}
	config.Paths = append([]string(nil), config.Paths...)
	config.Bundles = append([]skills.Bundle(nil), config.Bundles...)
	return &Library{config: config, store: skills.NewStore(nil)}, nil
}
func (e *Library) Store() *skills.Store { return e.store }
func (e *Library) Load(scope *extension.Scope) error {
	if err := scope.Init().Err(); err != nil {
		return err
	}
	loaded, _ := skills.LoadFrom(e.config.Directory, e.config.Paths, e.config.Catalog, e.config.Bundles...)
	*e.store = *loaded
	return scope.Init().Err()
}

// Skill data is immutable after profile initialization and has no open handles.
func (e *Library) Close(context.Context) error { return nil }
