package search

import (
	"strings"
	"testing"
)

func TestFormatTavilyResults(t *testing.T) {
	resp := tavilyResponse{
		Answer: "The answer is 42.",
		Results: []tavilyResult{
			{Title: "Result 1", URL: "https://example.com/1", Content: "Some content"},
		},
	}
	out := formatTavilyResults(resp, "test query")
	if !strings.Contains(out, "test query") {
		t.Fatalf("output missing query: %q", out)
	}
	if !strings.Contains(out, "The answer is 42.") {
		t.Fatalf("output missing answer: %q", out)
	}
	if !strings.Contains(out, "Result 1") {
		t.Fatalf("output missing result title: %q", out)
	}
}
