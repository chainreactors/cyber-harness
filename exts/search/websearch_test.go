package search

import (
	"strings"
	"testing"
)

func TestWebSearchRejectsUnverifiedResponses(t *testing.T) {
	for _, body := range []string{
		`{"output":[{"type":"message","content":[{"type":"output_text","text":"I cannot search live; likely Go 1.26","annotations":[]}]}]}`,
		`{"output":[{"type":"message","content":[{"type":"output_text","text":"Answer","annotations":[{"type":"url_citation","url":"https://go.dev"}]}]}]}`,
		`{"output":[{"type":"web_search_call","status":"failed"}]}`,
		`{"status":"incomplete","output":[{"type":"web_search_call","status":"completed"}]}`,
		`{}`,
	} {
		result, err := parseOpenAIWebSearchResponse([]byte(body), 3)
		if err == nil || result != nil {
			t.Fatalf("unverified response accepted: %s => %+v, %v", body, result, err)
		}
	}
}

func TestWebSearchAcceptsCompletedSearch(t *testing.T) {
	body := `{"status":"completed","output":[{"type":"web_search_call","status":"completed"},{"type":"message","content":[{"type":"output_text","text":"Verified answer","annotations":[{"type":"url_citation","title":"Go","url":"https://go.dev"}]},{"type":"output_text","text":"More","annotations":[{"type":"url_citation","url":"https://go.dev/dl"}]}]}]}`
	result, err := parseOpenAIWebSearchResponse([]byte(body), 1)
	if err != nil || len(result.Results) != 1 || !strings.Contains(result.Summary, "Verified answer") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
	// A completed search with no hits is valid; ordinary text without a search is not.
	result, err = parseOpenAIWebSearchResponse([]byte(`{"output":[{"type":"web_search_call","status":"completed"}]}`), 3)
	if err != nil || result == nil {
		t.Fatalf("empty search: result=%+v err=%v", result, err)
	}
}

func TestAnthropicSearchRequiresExecutedResult(t *testing.T) {
	for _, body := range []string{
		`{}`, `{"content":[{"type":"text","text":"an answer"}]}`,
		`{"content":[{"type":"web_search_tool_result","content":{"type":"web_search_tool_result_error","error_code":"unavailable"}}]}`,
		`{"content":[{"type":"web_search_tool_result","content":null}]}`,
	} {
		result, err := parseAnthropicWebSearchResponse([]byte(body))
		if err == nil || result != nil {
			t.Fatalf("accepted %s: %v %v", body, result, err)
		}
	}
	for _, content := range []string{`[]`, `[{"title":"Go","url":"https://go.dev"}]`} {
		result, err := parseAnthropicWebSearchResponse([]byte(`{"content":[{"type":"web_search_tool_result","content":` + content + `}]}`))
		if err != nil || result == nil {
			t.Fatalf("rejected search: %v %v", result, err)
		}
	}
}
