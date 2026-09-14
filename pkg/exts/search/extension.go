package search

import (
	"context"
	"fmt"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/toolset"
	searchtools "github.com/chainreactors/aiscan/tools/search"
	"github.com/chainreactors/sdk/pkg/association"
)

// Extension owns search tool declarations and command registrations.
type Extension struct {
	commands *commands.Registry
	tools    *toolset.Registry
	config   Config
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

func New(toolRegistry *toolset.Registry, cmdRegistry *commands.Registry, config Config) (*Extension, error) {
	if toolRegistry == nil || cmdRegistry == nil {
		return nil, fmt.Errorf("search requires tool and command registries")
	}
	return &Extension{tools: toolRegistry, commands: cmdRegistry, config: config}, nil
}

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
		DescriptionPath: "aiscan://skills/aiscan/okf/runtime/fetch.md",
		Run:             fetch.Run,
	}

	var index *association.Index
	if e.config.ResolveIndex != nil {
		index = e.config.ResolveIndex()
	}
	cyberhub := searchtools.NewCyberhubSearch(index)
	cyberhubCommand := commands.Command{
		Name: cyberhub.Name(), Usage: cyberhub.Usage(),
		DescriptionPath: "aiscan://skills/aiscan/okf/runtime/search.md",
		Run:             cyberhub.Run,
	}
	searchTool := searchtools.NewWebSearchTool(e.config.Search, tavily)
	entries := []commands.Command{fetchCommand, cyberhubCommand}
	if err := scope.Init().Err(); err != nil {
		return err
	}
	if err := e.tools.Register(scope, searchTool); err != nil {
		return err
	}
	if err := e.commands.Register(scope, "search", entries...); err != nil {
		return err
	}
	return nil
}

func (e *Extension) Close(context.Context) error { return nil }
