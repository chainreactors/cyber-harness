package provider

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
)

// A 404 on the chat endpoint must surface as an actionable protocol-mismatch
// hint (not a bare "API error (404): " with an empty body, which is what an
// Anthropic-only gateway returns for /chat/completions). The underlying
// *APIError must still unwrap so retry classification is unchanged.
func TestChatCompletion404GivesProtocolHint(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.WriteHeader(http.StatusNotFound) // empty body, like a wrong-protocol gateway
	}))
	defer srv.Close()

	cases := []struct {
		name         string
		provider     string
		wantProvider string // the provider the hint should steer the user toward
	}{
		{"openai endpoint 404 suggests anthropic", "openai", "llm.provider=anthropic"},
		{"anthropic endpoint 404 suggests openai", "anthropic", "llm.provider=openai"},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			p, err := NewProvider(&ProviderConfig{Provider: tc.provider, BaseURL: srv.URL + "/v1", APIKey: "k", Model: "m"})
			if err != nil {
				t.Fatalf("NewProvider: %v", err)
			}
			_, err = p.ChatCompletion(context.Background(), &ChatCompletionRequest{
				Messages: []*aop.Message{TextMessage("user", "hi")},
			})
			if err == nil {
				t.Fatal("expected a 404 error")
			}
			if !strings.Contains(err.Error(), tc.wantProvider) {
				t.Fatalf("error missing actionable hint %q: %v", tc.wantProvider, err)
			}
			// The wrapped *APIError must remain reachable and non-retryable.
			var apiErr *APIError
			if !errors.As(err, &apiErr) || apiErr.StatusCode != 404 {
				t.Fatalf("underlying *APIError(404) lost after wrapping: %v", err)
			}
			if apiErr.IsRetryable() {
				t.Fatal("a 404 must stay non-retryable")
			}
		})
	}
}

// The official Anthropic endpoint is unambiguous and must infer the anthropic
// protocol even with a blank provider; arbitrary gateways stay openai-default.
func TestInferFromBaseURLAnthropicOfficial(t *testing.T) {
	if got := InferFromBaseURL("https://api.anthropic.com/v1"); got != "anthropic" {
		t.Fatalf("InferFromBaseURL(api.anthropic.com) = %q, want anthropic", got)
	}
	if got := InferFromBaseURL("https://gateway.example.com/v1"); got != "openai" {
		t.Fatalf("custom gateway must still default to openai, got %q", got)
	}
}
