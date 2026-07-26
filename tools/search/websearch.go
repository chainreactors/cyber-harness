package search

import (
	"fmt"
	"strings"

	"github.com/chainreactors/aiscan/pkg/agent/provider"
)

func formatWebSearchResponse(resp *provider.WebSearchResponse, query string) string {
	var sb strings.Builder
	sb.WriteString(fmt.Sprintf("Web search results for: %s\n\n", query))

	if len(resp.Results) == 0 && resp.Summary == "" {
		sb.WriteString("No results found.\n")
		return sb.String()
	}

	for i, r := range resp.Results {
		sb.WriteString(fmt.Sprintf("[%d] %s\n    URL: %s\n\n", i+1, r.Title, r.URL))
	}

	if resp.Summary != "" {
		sb.WriteString("Summary:\n")
		sb.WriteString(resp.Summary)
		sb.WriteByte('\n')
	}
	return sb.String()
}
