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
	e.options.Keyless = slices.Clone(e.options.Keyless)
	if e.options.Credentials == nil {
		e.options.Credentials = map[string]string{}
	}
	var kinds []gateway.Kind
	for _, section := range passiveconfig.Sections() {
		name := section.Key
		kind := gateway.Kind{Config: section}
		if name == "fofa" {
			kind.Use = use
		}
		kind.Load = func(_ *extension.Scope, value any) (resource.Handle, error) {
			if value == nil {
				e.options.Disabled = append(e.options.Disabled, name)
				return nil, nil
			}
			var api cfg.API
			if name == "fofa" {
				config := value.(*passiveconfig.Fofa)
				api = config.API
				if config.Proxy != nil {
					e.options.Proxy = *config.Proxy
				}
				if config.Limit != nil {
					e.options.Limit = *config.Limit
				}
			} else {
				api = *value.(*cfg.API)
				if passiveconfig.Credential(name) == "" {
					// A keyless source is available only when configured.
					e.options.Keyless = append(e.options.Keyless, name)
				}
			}
			e.options.BaseURLs[name] = api.BaseURL
			if api.Key != "" {
				switch name {
				case "fofa":
					e.options.FofaKey = api.Key
				case "hunter":
					e.options.HunterAPIKey = api.Key
				default:
					e.options.Credentials[passiveconfig.Credential(name)] = api.Key
				}
			}
			return nil, nil
		}
		kinds = append(kinds, kind)
	}
	return kinds
}
