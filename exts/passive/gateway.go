package passive

import (
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/resource"
	"github.com/chainreactors/cyber/exts/gateway"
	passiveconfig "github.com/chainreactors/cyber/exts/passive/config"
)

// Gateway configures this owner before its command is loaded. No command or
// tool is added by the binding; subsequent host snapshots build a fresh owner.
func (e *Extension) Gateway(use string) gateway.Kind {
	return gateway.Kind{
		Config: passiveconfig.FofaSection(), Use: use,
		Load: func(_ *extension.Scope, value any) (resource.Handle, error) {
			if value == nil {
				e.options.FofaKey = ""
				return nil, nil
			}
			config := value.(*passiveconfig.Fofa)
			e.options.FofaKey, e.options.FofaURL = config.Key, config.URL
			if config.Proxy != nil {
				e.options.Proxy = *config.Proxy
			}
			if config.Limit != nil {
				e.options.Limit = *config.Limit
			}
			return nil, nil
		},
	}
}
