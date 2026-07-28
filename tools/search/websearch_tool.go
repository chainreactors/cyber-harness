package search

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/aiscan/agent/provider"
	"github.com/chainreactors/aiscan/core/tool"
)

type WebSearchTool struct {
	provider provider.Provider
	tavily   *TavilySearch
}

type webSearchArgs struct {
	Query string `json:"query"         jsonschema:"description=Search query (e.g. CVE-2024-1234 exploit)"`
	Num   int    `json:"num,omitempty"  jsonschema:"description=Max results 1-10 (default 5),minimum=1,maximum=10"`
}

func NewWebSearchTool(p provider.Provider, tavily *TavilySearch) *WebSearchTool {
	return &WebSearchTool{provider: p, tavily: tavily}
}

func (t *WebSearchTool) Name() string { return "web_search" }

func (t *WebSearchTool) Description() string {
	return "Search the web for CVEs, exploits, vulnerability details, and product documentation."
}

func (t *WebSearchTool) Definition() tool.Definition {
	return tool.Def("web_search", t.Description(), webSearchArgs{})
}

func (t *WebSearchTool) Execute(ctx context.Context, arguments string) (tool.Result, error) {
	args, err := tool.ParseArgs[webSearchArgs](arguments)
	if err != nil {
		return tool.Result{}, err
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" {
		return tool.Result{}, fmt.Errorf("query is required")
	}

	num := args.Num
	if num <= 0 {
		num = 5
	}
	if num > 10 {
		num = 10
	}

	if ws, ok := t.provider.(provider.WebSearchProvider); ok {
		resp, err := ws.WebSearch(ctx, args.Query, num)
		if err == nil {
			return tool.TextResult(formatWebSearchResponse(resp, args.Query)), nil
		}
	}

	if t.tavily != nil {
		result, err := t.tavily.Execute(ctx, []string{args.Query, "--num", fmt.Sprint(num)})
		if err == nil {
			return tool.TextResult(result), nil
		}
	}

	return tool.Result{}, fmt.Errorf("web_search: no search backend available. Configure Tavily API key via --tavily-key flag, env (TAVILY_API_KEY), or config file (search.tavily_keys). Do not retry until configured")
}
