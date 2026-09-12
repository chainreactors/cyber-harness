package search

import (
	"context"
	"fmt"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/resources"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/tools/scan/engine"
	searchtools "github.com/chainreactors/aiscan/tools/search"
	"github.com/chainreactors/sdk/pkg/association"
)

// Extension owns search tool declarations and command registrations.
type Extension struct {
	commands *commands.Registry
	tool     *searchtools.WebSearchTool
	entries  []commands.Command
	owner    string
}

func New(cmdRegistry *commands.Registry, search func(context.Context, string, int) (string, error), tavilyKeys, proxy, proxyCA string, engines *engine.Set, resourceSet *resources.Set) (*Extension, error) {
	if cmdRegistry == nil {
		return nil, fmt.Errorf("search requires commands")
	}
	tavily := searchtools.NewTavilySearch(tavilyKeys)
	if proxy != "" {
		tavily.SetProxy(proxy)
	}
	fetch := searchtools.NewFetchCommand().WithProxy(proxy).WithProxyCA(proxyCA)
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
	cyberhub := searchtools.NewCyberhubSearch(idx)
	cyberhubCommand := commands.Command{
		Name: cyberhub.Name(), Usage: cyberhub.Usage(),
		DescriptionPath: "aiscan://skills/aiscan/okf/runtime/search.md",
		Run:             cyberhub.Run,
	}
	return &Extension{commands: cmdRegistry, tool: searchtools.NewWebSearchTool(search, tavily), entries: []commands.Command{fetchCommand, cyberhubCommand}}, nil
}

func (e *Extension) Load(scope *extension.Context) error {
	if err := scope.Init().Err(); err != nil {
		return err
	}
	if err := scope.RegisterTools(e.tool); err != nil {
		return err
	}
	if err := e.commands.Register(scope.Owner(), "search", e.entries...); err != nil {
		return err
	}
	e.owner = scope.Owner()
	return nil
}

func (e *Extension) Close(ctx context.Context) error {
	if e.owner == "" {
		return nil
	}
	return e.commands.UnregisterOwner(ctx, e.owner)
}
