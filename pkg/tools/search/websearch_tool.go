package search

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/commands"
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

func (t *WebSearchTool) Definition() commands.ToolDefinition {
	return commands.ToolDef("web_search", t.Description(), webSearchArgs{})
}

func (t *WebSearchTool) Execute(ctx context.Context, arguments string) (commands.ToolResult, error) {
	args, err := commands.ParseArgs[webSearchArgs](arguments)
	if err != nil {
		return commands.ToolResult{}, err
	}
	args.Query = strings.TrimSpace(args.Query)
	if args.Query == "" {
		return commands.ToolResult{}, fmt.Errorf("query is required")
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
			return commands.TextResult(formatWebSearchResponse(resp, args.Query)), nil
		}
	}

	if t.tavily != nil {
		result, err := t.tavily.Execute(ctx, []string{args.Query, "--num", fmt.Sprint(num)})
		if err == nil {
			return commands.TextResult(result), nil
		}
	}

	return commands.ToolResult{}, fmt.Errorf("web_search: no search backend available. Configure Tavily API key via --tavily-key flag, env (TAVILY_API_KEY), or config file (search.tavily_keys). Do not retry until configured")
}
