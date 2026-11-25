package search

import (
	"context"
	"errors"
	"fmt"
	"strings"

	aop "github.com/chainreactors/cyber/aop"
	coretool "github.com/chainreactors/cyber/core/tool"
)

type WebSearchTool struct {
	search  func(context.Context, string, int) (string, error)
	backend *Client
}

type webSearchArgs struct {
	Query string `json:"query"         jsonschema:"description=Search query (e.g. Go context cancellation documentation)"`
	Num   int    `json:"num,omitempty"  jsonschema:"description=Max results 1-10 (default 5),minimum=1,maximum=10"`
}

func NewWebSearchTool(search func(context.Context, string, int) (string, error), backend *Client) *WebSearchTool {
	return &WebSearchTool{search: search, backend: backend}
}

func (t *WebSearchTool) Name() string { return "web_search" }

func (t *WebSearchTool) Description() string {
	return "Search the web for information, references, and documentation relevant to the task."
}

func (t *WebSearchTool) Definition() *aop.ToolDefinition {
	return coretool.Def("web_search", t.Description(), webSearchArgs{})
}

func (t *WebSearchTool) Execute(ctx context.Context, arguments string) (*aop.ToolResult, error) {
	args, err := coretool.ParseArgs[webSearchArgs](arguments)
	if err != nil {
		return nil, fmt.Errorf("web search was not executed: %w; correct the arguments and try again", err)
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" {
		return nil, errors.New("web search was not executed: query is required; provide a non-empty search query")
	}

	num := args.Num
	if num <= 0 {
		num = 5
	}
	if num > 10 {
		num = 10
	}

	var failures []error
	if t.search != nil {
		result, err := t.search(ctx, args.Query, num)
		if err == nil {
			return coretool.TextResult(result), nil
		}
		failures = append(failures, fmt.Errorf("provider search: %w", err))
	}

	if err := ctx.Err(); err != nil {
		return nil, errors.Join(append(failures, err)...)
	}
	if t.backend != nil {
		result, err := t.backend.Execute(ctx, []string{args.Query, "--num", fmt.Sprint(num)})
		if err == nil {
			return coretool.TextResult(result), nil
		}
		failures = append(failures, fmt.Errorf("fallback search: %w", err))
	}

	message := "Live web search failed; no verified search results were obtained. Do not present model knowledge or guessed URLs as search findings."
	message += "\nUse another available source, or explain that live search is unavailable. Configure a working search provider, Tavily API key (--tavily-key, TAVILY_API_KEY, search.tavily_keys), or network access for the fallback before retrying."
	if len(failures) == 0 {
		return nil, errors.New(message)
	}
	return nil, fmt.Errorf("%s: %w", message, errors.Join(failures...))
}
