package skills

import (
	"fmt"
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/resource"
	"path/filepath"
)

type LibraryConfig struct {
	Directory string
	Paths     []string
	Exclude   []string
	// ProjectDirs are the project-relative skill directories, in override
	// order. Nil selects the harness convention: .cyber/skills then
	// .agent/skills.
	ProjectDirs []skills.SkillDir
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
	if err := extension.Provide[*skills.Store](scope, e.store); err != nil {
		return err
	}
	projectDirs := e.config.ProjectDirs
	if projectDirs == nil {
		projectDirs = []skills.SkillDir{
			{Dir: ".cyber/skills", Source: skills.SourceProject},
			{Dir: ".agent/skills", Source: skills.SourceAgent},
		}
	}
	loaded, diagnostics := skills.LoadFrom(e.config.Directory, projectDirs, e.config.Paths)
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
	// Every distribution can read the neutral runtime tool documents.
	if err := extension.Add(scope, runtimeDocsBundle()); err != nil {
		return err
	}
	return scope.Init().Err()
}

var _ resource.Point[skills.Bundle] = (*skills.Store)(nil)
