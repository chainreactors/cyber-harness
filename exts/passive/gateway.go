package passive

import (
	"maps"
	"slices"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/resource"
	"github.com/chainreactors/cyber/exts/gateway"
	passiveconfig "github.com/chainreactors/cyber/exts/passive/config"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

// Gateway configures this owner before its command is loaded. No command or
// tool is added by the binding; subsequent host snapshots build a fresh owner.
func (e *Extension) Gateway(use string) []gateway.Kind {
	e.options.BaseURLs = maps.Clone(e.options.BaseURLs)
	if e.options.BaseURLs == nil {
		e.options.BaseURLs = map[string]string{}
	}
	e.options.Credentials = maps.Clone(e.options.Credentials)
	e.options.Disabled = slices.Clone(e.options.Disabled)
	if e.options.Credentials == nil {
		e.options.Credentials = map[string]string{}
	}
	kinds := []gateway.Kind{{
		Config: passiveconfig.FofaSection(), Use: use,
		Load: func(_ *extension.Scope, value any) (resource.Handle, error) {
			if value == nil {
				e.options.Disabled = append(e.options.Disabled, "fofa")
				return nil, nil
			}
			config := value.(*passiveconfig.Fofa)
			e.options.FofaKey, e.options.BaseURLs["fofa"] = config.Key, config.BaseURL
			if config.Proxy != nil {
				e.options.Proxy = *config.Proxy
			}
			if config.Limit != nil {
				e.options.Limit = *config.Limit
			}
			return nil, nil
		},
	}}
	for _, section := range passiveconfig.Sections() {
		if section.Key == "fofa" {
			continue
		}
		name := section.Key
		kinds = append(kinds, gateway.Kind{Config: section, Load: func(_ *extension.Scope, value any) (resource.Handle, error) {
			if value == nil {
				e.options.Disabled = append(e.options.Disabled, name)
				return nil, nil
			}
			config := value.(*cfg.API)
			e.options.BaseURLs[name] = config.BaseURL
			if config.Key != "" {
				if name == "hunter" {
					e.options.HunterAPIKey = config.Key
				} else {
					e.options.Credentials[passiveconfig.Credential(name)] = config.Key
				}
			}
			return nil, nil
		}})
	}
	return kinds
}
