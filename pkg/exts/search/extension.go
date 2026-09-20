package search

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/cyber/agent/provider"

	"github.com/chainreactors/cyber/core/egress"
	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"

	searchtools "github.com/chainreactors/cyber/tools/search"
)

// Extension owns search tool declarations and command registrations.
type Extension struct {
	config        Config
	executionOnly bool
}

type Config struct {
	TavilyKeys string
}

func New(config Config) *Extension { return &Extension{config: config} }

// NewExecution uses the configured non-model search backend only.
func NewExecution(config Config) *Extension { return &Extension{config: config, executionOnly: true} }

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
	tavily := searchtools.NewTavilySearch(e.config.TavilyKeys)
	if proxy != "" {
		tavily.SetProxy(proxy)
	}
	fetch := searchtools.NewFetchCommand().WithProxy(proxy).WithProxyCA(proxyCA)
	fetchCommand := coretool.Command{
		Name: fetch.Name(), Usage: fetch.Usage(),
		DescriptionPath: "cyber://skills/cyber/okf/runtime/fetch.md",
		Run:             fetch.Run,
	}

	searchTool := searchtools.NewWebSearchTool(providerWebSearch(application), tavily)
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

// providerWebSearch adapts the configured model's own web search, when it has
// one, to the search tool's signature. It lives here because it is search
// behavior, not composition.
func providerWebSearch(application *provider.State) func(context.Context, string, int) (string, error) {
	if application == nil {
		return nil
	}
	model, _ := application.Current()
	searcher, ok := model.(provider.WebSearchProvider)
	if !ok {
		return nil
	}
	return func(ctx context.Context, query string, maxResults int) (string, error) {
		response, err := searcher.WebSearch(ctx, query, maxResults)
		if err != nil {
			return "", err
		}
		var text strings.Builder
		fmt.Fprintf(&text, "Web search results for: %s\n\n", query)
		if len(response.Results) == 0 && response.Summary == "" {
			text.WriteString("No results found.\n")
			return text.String(), nil
		}
		for index, result := range response.Results {
			fmt.Fprintf(&text, "[%d] %s\n    URL: %s\n\n", index+1, result.Title, result.URL)
		}
		if response.Summary != "" {
			text.WriteString("Summary:\n")
			text.WriteString(response.Summary)
			text.WriteByte('\n')
		}
		return text.String(), nil
	}
}
