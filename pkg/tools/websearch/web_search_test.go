package websearch

import (
	"strings"
	"testing"
)

func TestParseArgsBasicQuery(t *testing.T) {
	query, num, err := parseArgs([]string{"CVE-2024-1234", "exploit"}, "")
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if query != "CVE-2024-1234 exploit" {
		t.Fatalf("query = %q, want %q", query, "CVE-2024-1234 exploit")
	}
	if num != defaultNumResults {
		t.Fatalf("num = %d, want %d", num, defaultNumResults)
	}
}

func TestParseArgsWithNum(t *testing.T) {
	query, num, err := parseArgs([]string{"nginx", "--num", "8"}, "")
	if err != nil {
		t.Fatalf("parseArgs() error = %v", err)
	}
	if query != "nginx" {
		t.Fatalf("query = %q", query)
	}
	if num != 8 {
		t.Fatalf("num = %d, want 8", num)
	}
}

func TestParseArgsClampsNum(t *testing.T) {
	_, num, err := parseArgs([]string{"test", "--num", "999"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if num != maxNumResults {
		t.Fatalf("num = %d, want clamped to %d", num, maxNumResults)
	}

	_, num, err = parseArgs([]string{"test", "--num", "0"}, "")
	if err != nil {
		t.Fatal(err)
	}
	if num != defaultNumResults {
		t.Fatalf("num = %d, want default %d", num, defaultNumResults)
	}
}

func TestParseArgsRequiresQuery(t *testing.T) {
	_, _, err := parseArgs([]string{}, "usage")
	if err == nil {
		t.Fatal("expected error for empty args")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Fatalf("error = %q", err)
	}
}

func TestParseArgsRejectsUnknownFlag(t *testing.T) {
	_, _, err := parseArgs([]string{"test", "--bad"}, "")
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}

func TestExtractDDGResults(t *testing.T) {
	html := `
	<div class="result">
		<a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpage">Example Page</a>
		<a class="result__snippet">This is a snippet about the page.</a>
	</div>
	<div class="result">
		<a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fother.com%2Finfo">Other Info</a>
		<a class="result__snippet">Another snippet here.</a>
	</div>`

	results := extractDDGResults(html, 10)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
	if results[0].URL != "https://example.com/page" {
		t.Fatalf("results[0].URL = %q", results[0].URL)
	}
	if results[0].Title != "Example Page" {
		t.Fatalf("results[0].Title = %q", results[0].Title)
	}
	if results[1].URL != "https://other.com/info" {
		t.Fatalf("results[1].URL = %q", results[1].URL)
	}
}

func TestExtractDDGResultsRespectsLimit(t *testing.T) {
	html := `
	<a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fa.com">A</a>
	<a class="result__snippet">aa</a>
	<a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fb.com">B</a>
	<a class="result__snippet">bb</a>
	<a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fc.com">C</a>
	<a class="result__snippet">cc</a>`

	results := extractDDGResults(html, 2)
	if len(results) != 2 {
		t.Fatalf("got %d results, want 2", len(results))
	}
}

func TestResolveDDGURL(t *testing.T) {
	tests := []struct {
		input string
		want  string
	}{
		{"https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com", "https://example.com"},
		{"https://example.com/direct", "https://example.com/direct"},
		{"//example.com/path", "https://example.com/path"},
		{"", ""},
	}
	for _, tt := range tests {
		got := resolveDDGURL(tt.input)
		if got != tt.want {
			t.Errorf("resolveDDGURL(%q) = %q, want %q", tt.input, got, tt.want)
		}
	}
}

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
