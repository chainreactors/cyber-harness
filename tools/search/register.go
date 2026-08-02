package search

import (
	"github.com/chainreactors/aiscan/core/capability"
	"github.com/chainreactors/aiscan/core/deps"
	"github.com/chainreactors/aiscan/core/resources"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan/engine"
	"github.com/chainreactors/sdk/pkg/association"
)

func init() {
	capability.Register(capability.Descriptor{
		ID: "search", Kind: capability.KindTool, Group: "search",
		Optional: true, Default: true,
	})
	commands.RegisterFactory(commands.Factory{
		Capability: "search",
		Build: func(d *commands.Deps, reg *commands.CommandRegistry) {
			tavily := NewTavilySearch(d.TavilyKeys)
			if d.ScannerProxy != "" {
				tavily.SetProxy(d.ScannerProxy)
			}

			reg.RegisterTool(NewWebSearchTool(d.Provider, tavily))
			fetch := NewFetchCommand()
			reg.Register(commands.Command{
				Name: fetch.Name(), Usage: fetch.Usage(),
				DescriptionPath: "aiscan://skills/aiscan/okf/runtime/fetch.md",
				Run:             fetch.Run,
			}, "search")

			var idx *association.Index
			if es, ok := deps.Get(d.Bag, engine.SetKey); ok && es != nil {
				idx = es.Index
			}
			if idx == nil {
				if rs, ok := deps.Get(d.Bag, resources.SetKey); ok && rs != nil && rs.FingersConfig != nil {
					full := rs.FingersConfig.FullFingers
					idx = association.NewIndex()
					idx.BuildWithFingers(full.Fingers(), full.Aliases(), nil)
				}
			}
			if idx == nil {
				// cyberhub still answers, but without local fingerprint association.
				d.Skip("cyberhub.index", deps.Name(engine.SetKey)+"/"+deps.Name(resources.SetKey))
			}
			cyberhub := NewCyberhubSearch(idx)
			reg.Register(commands.Command{
				Name: cyberhub.Name(), Usage: cyberhub.Usage(),
				DescriptionPath: "aiscan://skills/aiscan/okf/runtime/search.md",
				Run:             cyberhub.Run,
			}, "search")
		},
	})
}
