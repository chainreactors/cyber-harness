package search

import (
	"context"
	"fmt"

	"github.com/chainreactors/cyber/agent/provider"

	"github.com/chainreactors/cyber/core/egress"
	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"

	searchtools "github.com/chainreactors/cyber/tools/search"
)

// Extension owns search tool declarations and command registrations.
type Extension struct {
	config        Options
	executionOnly bool
	search        *modelSearch
	backend       searchtools.Config
}

func New(config Options) *Extension { return &Extension{config: config} }

// NewExecution uses the configured non-model search backend only.
func NewExecution(config Options) *Extension { return &Extension{config: config, executionOnly: true} }

func (e *Extension) Load(scope *extension.Scope) error {
	if scope == nil {
		return fmt.Errorf("search extension context is required")
	}
	endpoint, err := extension.Use[egress.Endpoint](scope)
	if err != nil {
		return err
	}
	var application *provider.State
	if !e.executionOnly {
		application, err = extension.Use[*provider.State](scope)
		if err != nil {
			return err
		}
	}
	proxy, proxyCA := endpoint.ProxyURL(), endpoint.CAPath()
	backend := e.backend
	if backend.Keys == "" {
		backend.Keys = e.config.TavilyKeys
	}
	backend.Proxy = proxy
	tavily := searchtools.New(backend)
	fetch := searchtools.NewFetchCommand().WithProxy(proxy).WithProxyCA(proxyCA)
	fetchCommand := coretool.Command{
		Name: fetch.Name(), Usage: fetch.Usage(),
		DescriptionPath: "cyber://skills/runtime/fetch.md",
		Run:             fetch.Run,
	}

	var search func(context.Context, string, int) (string, error)
	if application != nil {
		model, config := application.Current()
		if model != nil {
			e.search, err = newModelSearch(config)
			if err != nil {
				return err
			}
			search = e.search.search
		}
	}
	searchTool := searchtools.NewWebSearchTool(search, tavily)
	entries := []coretool.Command{fetchCommand}
	if err := scope.Init().Err(); err != nil {
		return err
	}
	if err := extension.Add[coretool.Tool](scope, searchTool); err != nil {
		return err
	}
	if err := extension.Add(scope, entries...); err != nil {
		return err
	}
	return nil
}

func (e *Extension) Close(context.Context) error {
	if e.search != nil {
		e.search.client.CloseIdleConnections()
	}
	return nil
}
