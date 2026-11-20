package search

import "testing"

func TestExtractDDGResults(t *testing.T) {
	html := `
	<div class="result">
		<a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fexample.com%2Fpage">Example Page</a>
		<a class="result__snippet">This is a snippet about the page.</a>
	</div>
	<div class="result">
		<a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fother.example%2Finfo">Other Info</a>
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
	if results[1].URL != "https://other.example/info" {
		t.Fatalf("results[1].URL = %q", results[1].URL)
	}
}

func TestExtractDDGResultsRespectsLimit(t *testing.T) {
	html := `
	<a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fa.example">A</a>
	<a class="result__snippet">aa</a>
	<a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fb.example">B</a>
	<a class="result__snippet">bb</a>
	<a class="result__a" href="https://duckduckgo.com/l/?uddg=https%3A%2F%2Fc.example">C</a>
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
