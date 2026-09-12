package search

import (
	"context"
	"errors"
	"fmt"

	"github.com/chainreactors/aiscan/core/resources"
	coretool "github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan/engine"
	"github.com/chainreactors/sdk/pkg/association"
)

func Register(cmdRegistry *commands.Registry, tools coretool.Registrar, search func(context.Context, string, int) (string, error), tavilyKeys, proxy, proxyCA string, engines *engine.Set, resourceSet *resources.Set) error {
	if cmdRegistry == nil || tools == nil {
		return fmt.Errorf("search requires command and tool registries")
	}
	tavily := NewTavilySearch(tavilyKeys)
	if proxy != "" {
		tavily.SetProxy(proxy)
	}
	lease, err := tools.Register("search", NewWebSearchTool(search, tavily))
	if err != nil {
		return err
	}
	fetch := NewFetchCommand().WithProxy(proxy).WithProxyCA(proxyCA)
	fetchCommand := commands.Command{
		Name: fetch.Name(), Usage: fetch.Usage(),
		DescriptionPath: "aiscan://skills/aiscan/okf/runtime/fetch.md",
		Run:             fetch.Run,
	}

	var idx *association.Index
	if engines != nil {
		idx = engines.Index
	}
	if idx == nil && resourceSet != nil && resourceSet.FingersConfig != nil {
		full := resourceSet.FingersConfig.FullFingers
		idx = association.NewIndex()
		idx.BuildWithFingers(full.Fingers(), full.Aliases(), nil)
	}
	cyberhub := NewCyberhubSearch(idx)
	cyberhubCommand := commands.Command{
		Name: cyberhub.Name(), Usage: cyberhub.Usage(),
		DescriptionPath: "aiscan://skills/aiscan/okf/runtime/search.md",
		Run:             cyberhub.Run,
	}
	if err := cmdRegistry.Register("search", "search", fetchCommand, cyberhubCommand); err != nil {
		return errors.Join(err, lease.Close(context.Background()))
	}
	return nil
}
