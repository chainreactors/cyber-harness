package search

import (
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/chainreactors/cyber/agent/provider"
)

// Search is independent of the chat protocol and model. Recognize the official
// host or an explicit vendor alias, never just a model name on another gateway.
func deepSeekSearchEndpoint(cfg provider.ProviderConfig) string {
	endpoint, err := url.Parse(cfg.BaseURL)
	if err != nil || (provider.NormalizeProvider(cfg.Provider) != "deepseek" && !strings.EqualFold(endpoint.Hostname(), "api.deepseek.com")) {
		return ""
	}
	if strings.EqualFold(endpoint.Hostname(), "api.deepseek.com") {
		endpoint.Path = "/v1/tools/search"
	} else {
		// Preserve a custom gateway's prefix and origin.
		base := strings.TrimRight(endpoint.Path, "/")
		base = strings.TrimSuffix(base, "/v1")
		endpoint.Path = base + "/v1/tools/search"
	}
	endpoint.RawPath = ""
	return endpoint.String()
}

func parseDeepSeekWebSearchResponse(data []byte, maxResults int) (*searchResponse, error) {
	var raw struct {
		Results []searchResult     `json:"results"`
		Error   *provider.APIError `json:"error"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse DeepSeek web search response: %w", err)
	}
	if raw.Error != nil {
		return nil, raw.Error
	}
	// An explicit empty array is a successful search with no hits. A chat answer,
	// missing field, or null is not evidence that a search was performed.
	if raw.Results == nil {
		return nil, fmt.Errorf("DeepSeek web search was not executed: no results array")
	}
	if len(raw.Results) > maxResults {
		raw.Results = raw.Results[:maxResults]
	}
	for i := range raw.Results {
		result := &raw.Results[i]
		if strings.TrimSpace(result.URL) == "" {
			return nil, fmt.Errorf("DeepSeek web search result has no URL")
		}
		if strings.TrimSpace(result.Title) == "" {
			result.Title = result.URL
		}
	}
	return &searchResponse{Results: raw.Results}, nil
}
