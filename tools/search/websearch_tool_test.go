package search

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	coretool "github.com/chainreactors/cyber/core/tool"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
)

func TestWebSearchFailureReturnsModelFeedback(t *testing.T) {
	backend := New(Config{})
	backend.client = &http.Client{Transport: searchFailureTransport{}}
	web := NewWebSearchTool(func(context.Context, string, int) (string, error) {
		return "", fmt.Errorf("no completed web_search_call")
	}, backend)
	for _, args := range []string{`{"query":"Go release"}`, `{"query":" "}`, `{`} {
		result, err := web.Execute(t.Context(), args)
		if err == nil {
			t.Fatal("expected an execution error")
		}
		result = toolhooks.Feedback(result, err)
		if result == nil || !result.IsError || result.Terminate {
			t.Fatalf("result=%+v err=%v", result, err)
		}
		if args == `{"query":"Go release"}` {
			text := coretool.ResultText(result)
			for _, want := range []string{"no completed web_search_call", "network unavailable", "Do not present"} {
				if !strings.Contains(text, want) {
					t.Fatalf("missing %q: %s", want, text)
				}
			}
		}
	}
}

type searchFailureTransport struct{}

func (searchFailureTransport) RoundTrip(*http.Request) (*http.Response, error) {
	return nil, fmt.Errorf("network unavailable")
}

func TestWebSearchUnverifiedProviderFallsBack(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		fmt.Fprint(w, `<a class="result__a" href="https://go.dev/dl/">Go downloads</a>`)
	}))
	defer server.Close()
	backend := New(Config{DDGBaseURL: server.URL})
	web := NewWebSearchTool(func(context.Context, string, int) (string, error) {
		return "", fmt.Errorf("no completed web_search_call")
	}, backend)
	result, err := web.Execute(t.Context(), `{"query":"Go release"}`)
	if err != nil || result == nil || result.IsError || !strings.Contains(coretool.ResultText(result), "https://go.dev/dl/") {
		t.Fatalf("result=%+v err=%v", result, err)
	}
}

func TestSearchCancellationStopsFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(t.Context())
	web := NewWebSearchTool(func(context.Context, string, int) (string, error) { cancel(); return "", context.Canceled }, New(Config{}))
	_, err := web.Execute(ctx, `{"query":"Go"}`)
	if !errors.Is(err, context.Canceled) || strings.Contains(err.Error(), "fallback search") {
		t.Fatalf("err=%v", err)
	}
}

func TestSearchAcceptsZeroHits(t *testing.T) {
	web := NewWebSearchTool(func(context.Context, string, int) (string, error) { return "", nil }, nil)
	result, err := web.Execute(t.Context(), `{"query":"no hits"}`)
	if err != nil || result == nil || result.IsError {
		t.Fatalf("result=%v err=%v", result, err)
	}
}
