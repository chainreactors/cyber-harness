package search

import (
	"context"
	"fmt"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/pkg/commands"
	searchtools "github.com/chainreactors/cyber/tools/search"
	"github.com/chainreactors/sdk/pkg/association"
)

// Extension owns search tool declarations and command registrations.
type Extension struct {
	config Config
}

type ProxyEndpoint interface {
	ProxyURL() string
	CAPath() string
}

type Config struct {
	Search     func(context.Context, string, int) (string, error)
	TavilyKeys string
	Proxy      ProxyEndpoint
	// ResolveIndex is evaluated during Load, after any engine dependency has
	// published its association index. Nil installs the command with no catalog.
	ResolveIndex func() *association.Index
}

func New(config Config) *Extension { return &Extension{config: config} }

func (e *Extension) Load(scope *extension.Scope) error {
	if scope == nil {
		return fmt.Errorf("search extension context is required")
	}
	var proxy, proxyCA string
	if e.config.Proxy != nil {
		proxy, proxyCA = e.config.Proxy.ProxyURL(), e.config.Proxy.CAPath()
	}
	tavily := searchtools.NewTavilySearch(e.config.TavilyKeys)
	if proxy != "" {
		tavily.SetProxy(proxy)
	}
	fetch := searchtools.NewFetchCommand().WithProxy(proxy).WithProxyCA(proxyCA)
	fetchCommand := commands.Command{
		Name: fetch.Name(), Usage: fetch.Usage(),
		DescriptionPath: "cyber://skills/cyber/okf/runtime/fetch.md",
		Run:             fetch.Run,
	}

	var index *association.Index
	if e.config.ResolveIndex != nil {
		index = e.config.ResolveIndex()
	}
	cyberhub := searchtools.NewCyberhubSearch(index)
	cyberhubCommand := commands.Command{
		Name: cyberhub.Name(), Usage: cyberhub.Usage(),
		DescriptionPath: "cyber://skills/cyber/okf/runtime/search.md",
		Run:             cyberhub.Run,
	}
	searchTool := searchtools.NewWebSearchTool(e.config.Search, tavily)
	entries := []commands.Command{fetchCommand, cyberhubCommand}
	if err := scope.Init().Err(); err != nil {
		return err
	}
	if err := extension.Add[tool.Tool](scope, searchTool); err != nil {
		return err
	}
	if err := extension.Add(scope, entries...); err != nil {
		return err
	}
	return nil
}
