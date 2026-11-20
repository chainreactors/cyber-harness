package search

import (
	"fmt"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/resource"
	"github.com/chainreactors/cyber/exts/gateway"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func Sections() []cfg.Section {
	var sections []cfg.Section
	for _, name := range []string{"tavily", "duckduckgo"} {
		sections = append(sections, apiSection(name))
	}
	return sections
}

func apiSection(name string) cfg.Section {
	return cfg.Section{Key: name, New: func() any { return &cfg.API{} }, Secrets: []string{"key"}, Validate: func(value any) error {
		api := value.(*cfg.API)
		if name == "duckduckgo" && api.Key != "" {
			return fmt.Errorf("duckduckgo does not use an API key")
		}
		return cfg.ValidateBaseURL(api.BaseURL)
	}}
}

func (e *Extension) Gateway() []gateway.Kind {
	var kinds []gateway.Kind
	for _, section := range Sections() {
		name := section.Key
		kinds = append(kinds, gateway.Kind{Config: section, Load: func(_ *extension.Scope, value any) (resource.Handle, error) {
			if name == "tavily" {
				e.backend.DisableTavily = value == nil
				if value != nil {
					api := value.(*cfg.API)
					e.backend.Keys, e.backend.BaseURL = api.Key, api.BaseURL
				}
			} else {
				e.backend.DisableDDG = value == nil
				if value != nil {
					e.backend.DDGBaseURL = value.(*cfg.API).BaseURL
				}
			}
			return nil, nil
		}})
	}
	return kinds
}
