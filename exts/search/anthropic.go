package search

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chainreactors/cyber/agent/provider"
)

func parseAnthropicWebSearchResponse(data []byte) (*searchResponse, error) {
	var probe struct {
		Type  string             `json:"type"`
		Error *provider.APIError `json:"error,omitempty"`
	}
	if err := json.Unmarshal(data, &probe); err == nil && probe.Type == "error" && probe.Error != nil {
		return nil, probe.Error
	}

	var raw struct {
		Content []struct {
			Type    string          `json:"type"`
			Text    string          `json:"text,omitempty"`
			Content json.RawMessage `json:"content,omitempty"`
		} `json:"content"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse web search response: %w", err)
	}

	out := &searchResponse{}
	searched := false
	for _, block := range raw.Content {
		switch block.Type {
		case "web_search_tool_result":
			var results []struct {
				Title string `json:"title"`
				URL   string `json:"url"`
			}
			if len(block.Content) == 0 || block.Content[0] != '[' {
				return nil, fmt.Errorf("web search failed: %s", block.Content)
			}
			if err := json.Unmarshal(block.Content, &results); err != nil {
				return nil, fmt.Errorf("parse web search results: %w", err)
			}
			searched = true
			for _, r := range results {
				if r.URL == "" {
					continue
				}
				out.Results = append(out.Results, searchResult{Title: r.Title, URL: r.URL})
			}
		case "text":
			if t := strings.TrimSpace(block.Text); t != "" {
				out.Summary += t + "\n"
			}
		}
	}
	if !searched {
		return nil, fmt.Errorf("web search was not executed: no web_search_tool_result")
	}
	out.Summary = strings.TrimSpace(out.Summary)
	return out, nil
}
