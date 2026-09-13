package search

import (
	"context"
	"fmt"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/toolset"
	searchtools "github.com/chainreactors/aiscan/tools/search"
)

// Extension owns search tool declarations and command registrations.
type Extension struct {
	commands *commands.Registry
	tools    *toolset.Registry
	config   Config
	tool     *searchtools.WebSearchTool
	entries  []commands.Command
}

type ProxyEndpoint interface {
	ProxyURL() string
	CAPath() string
}

type Config struct {
	Search     func(context.Context, string, int) (string, error)
	TavilyKeys string
	Proxy      ProxyEndpoint
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

	cyberhub := searchtools.NewCyberhubSearch(nil)
	cyberhubCommand := commands.Command{
		Name: cyberhub.Name(), Usage: cyberhub.Usage(),
		DescriptionPath: "aiscan://skills/aiscan/okf/runtime/search.md",
		Run:             cyberhub.Run,
	}
	e.tool = searchtools.NewWebSearchTool(e.config.Search, tavily)
	e.entries = []commands.Command{fetchCommand, cyberhubCommand}
	if err := scope.Init().Err(); err != nil {
		return err
	}
	if err := e.tools.Register(scope, e.tool); err != nil {
		return err
	}
	if err := e.commands.Register(scope, "search", e.entries...); err != nil {
		return err
	}
	return nil
}

func (e *Extension) Close(context.Context) error { return nil }
