package skills

import (
	"fmt"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/resource"
	"github.com/chainreactors/cyber/pkg/skills"
	"path/filepath"
)

type LibraryConfig struct {
	Directory string
	Paths     []string
	Exclude   []string
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
	config.Exclude = append([]string(nil), config.Exclude...)
	return &Library{config: config, store: skills.NewStore(nil)}, nil
}
func (e *Library) Store() *skills.Store { return e.store }
func (e *Library) Load(scope *extension.Scope) error {
	if err := scope.Init().Err(); err != nil {
		return err
	}
	loaded, diagnostics := skills.LoadFrom(e.config.Directory, e.config.Paths)
	values := loaded.All()
	if len(e.config.Exclude) > 0 {
		excluded := make(map[string]bool, len(e.config.Exclude))
		for _, name := range e.config.Exclude {
			excluded[name] = true
		}
		filtered := values[:0]
		for _, value := range values {
			if !excluded[value.Name] {
				filtered = append(filtered, value)
			}
		}
		values = filtered
	}
	e.store.Replace(values, diagnostics)
	if err := extension.Define[skills.Bundle](scope, e.store); err != nil {
		return err
	}
	return scope.Init().Err()
}

var _ resource.Point[skills.Bundle] = (*skills.Store)(nil)
