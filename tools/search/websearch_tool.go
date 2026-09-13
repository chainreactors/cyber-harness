package search

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/aiscan/core/tool"
)

type WebSearchTool struct {
	search func(context.Context, string, int) (string, error)
	tavily *TavilySearch
}

type webSearchArgs struct {
	Query string `json:"query"         jsonschema:"description=Search query (e.g. CVE-2024-1234 exploit)"`
	Num   int    `json:"num,omitempty"  jsonschema:"description=Max results 1-10 (default 5),minimum=1,maximum=10"`
}

func NewWebSearchTool(search func(context.Context, string, int) (string, error), tavily *TavilySearch) *WebSearchTool {
	return &WebSearchTool{search: search, tavily: tavily}
}

func (t *WebSearchTool) Name() string { return "web_search" }

func (t *WebSearchTool) Description() string {
	return "Search the web for CVEs, exploits, vulnerability details, and product documentation."
}

func (t *WebSearchTool) Definition() *tool.Definition {
	return tool.Def("web_search", t.Description(), webSearchArgs{})
}

func (t *WebSearchTool) Execute(ctx context.Context, arguments string) (*tool.Result, error) {
	args, err := tool.ParseArgs[webSearchArgs](arguments)
	if err != nil {
		return nil, err
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" {
		return nil, fmt.Errorf("query is required")
	}

	num := args.Num
	if num <= 0 {
		num = 5
	}
	if num > 10 {
		num = 10
	}

	if t.search != nil {
		result, err := t.search(ctx, args.Query, num)
		if err == nil && strings.TrimSpace(result) != "" {
			return tool.TextResult(result), nil
		}
	}

	if t.tavily != nil {
		result, err := t.tavily.Execute(ctx, []string{args.Query, "--num", fmt.Sprint(num)})
		if err == nil {
			return tool.TextResult(result), nil
		}
	}

	return nil, fmt.Errorf("web_search: no search backend available. Configure Tavily API key via --tavily-key flag, env (TAVILY_API_KEY), or config file (search.tavily_keys). Do not retry until configured")
}
