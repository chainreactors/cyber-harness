package search

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chainreactors/cyber/agent/provider"
)

func parseOpenAIWebSearchResponse(data []byte, maxResults int) (*searchResponse, error) {
	var probe struct {
		Error *provider.APIError `json:"error,omitempty"`
	}
	if json.Unmarshal(data, &probe) == nil && probe.Error != nil {
		return nil, probe.Error
	}

	var raw struct {
		Status string `json:"status"`
		Output []struct {
			Status  string `json:"status"`
			Type    string `json:"type"`
			Content []struct {
				Type        string `json:"type"`
				Text        string `json:"text"`
				Annotations []struct {
					Type  string `json:"type"`
					Title string `json:"title"`
					URL   string `json:"url"`
				} `json:"annotations,omitempty"`
			} `json:"content,omitempty"`
		} `json:"output"`
	}
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse web search response: %w", err)
	}

	searched := false
	for _, block := range raw.Output {
		if block.Type == "web_search_call" && block.Status == "completed" {
			searched = true
		}
	}
	if !searched || (raw.Status != "" && raw.Status != "completed") {
		return nil, fmt.Errorf("web search was not completed: no completed web_search_call or incomplete response (status=%q)", raw.Status)
	}
	out := &searchResponse{}
	seen := make(map[string]struct{})
	for _, block := range raw.Output {
		if block.Type != "message" {
			continue
		}
		for _, c := range block.Content {
			if c.Type == "output_text" && strings.TrimSpace(c.Text) != "" {
				out.Summary += c.Text + "\n"
			}
			for _, ann := range c.Annotations {
				if ann.Type != "url_citation" || ann.URL == "" {
					continue
				}
				if _, ok := seen[ann.URL]; ok || len(out.Results) >= maxResults {
					continue
				}
				seen[ann.URL] = struct{}{}
				title := ann.Title
				if title == "" {
					title = ann.URL
				}
				out.Results = append(out.Results, searchResult{Title: title, URL: ann.URL})
				if len(out.Results) >= maxResults {
					break
				}
			}
		}
	}
	out.Summary = strings.TrimSpace(out.Summary)
	return out, nil
}
