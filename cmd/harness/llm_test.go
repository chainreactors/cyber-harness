//go:build live_llm

package harness_test

import (
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"testing"
)

func liveLLMRequest(t *testing.T) map[string]any {
	t.Helper()
	key := strings.TrimSpace(os.Getenv("AISCAN_HARNESS_LLM_API_KEY"))
	model := strings.TrimSpace(os.Getenv("AISCAN_HARNESS_LLM_MODEL"))
	provider := strings.TrimSpace(os.Getenv("AISCAN_HARNESS_LLM_PROVIDER"))
	baseURL := strings.TrimSpace(os.Getenv("AISCAN_HARNESS_LLM_BASE_URL"))
	if key == "" || model == "" || baseURL == "" {
		t.Fatal("live_llm requires AISCAN_HARNESS_LLM_API_KEY, AISCAN_HARNESS_LLM_MODEL and AISCAN_HARNESS_LLM_BASE_URL")
	}
	endpoint, err := url.Parse(baseURL)
	if err != nil || endpoint.Host == "" || (endpoint.Scheme != "https" && endpoint.Scheme != "http") || endpoint.User != nil || endpoint.RawQuery != "" {
		t.Fatal("LLM base URL must be an HTTP(S) endpoint without URL credentials or query parameters")
	}
	if provider == "" {
		provider = "openai"
	}
	return map[string]any{"provider": provider, "baseUrl": baseURL, "apiKey": key, "model": model}
}

func assertLiveReply(t *testing.T, result map[string]any) {
	t.Helper()
	if ok, _ := field(result, "ok").(bool); !ok {
		t.Fatalf("real LLM completion failed: %v", result)
	}
	if reply, _ := field(result, "reply").(string); strings.TrimSpace(reply) == "" {
		t.Fatal("real LLM returned an empty reply")
	}
}

func TestLiveLLMRecoveryAcrossRestart(t *testing.T) {
	request := liveLLMRequest(t)
	w := newWorkspace(t)
	p := w.startMode(t, true)
	user := p.user(t, "llm-user")
	assertLiveReply(t, user.call(t, http.MethodPost, configRPC+"TestLLM", request, http.StatusOK))
	status := user.call(t, http.MethodPost, "/aiscan.rpc.system.SystemService/GetStatus", map[string]any{}, http.StatusOK)
	if available, _ := field(status, "status", "llmAvailable").(bool); !available {
		t.Fatalf("product status did not report the configured provider: %v", status)
	}
	t.Log("a missing model produces an actionable failure and the user can retry")
	invalid := map[string]any{"provider": request["provider"], "baseUrl": request["baseUrl"], "apiKey": request["apiKey"]}
	failed := user.call(t, http.MethodPost, configRPC+"TestLLM", invalid, http.StatusOK)
	if ok, _ := field(failed, "ok").(bool); ok || field(failed, "error") == nil || field(failed, "error") == "" {
		t.Fatalf("missing-model check did not fail clearly: %v", failed)
	}
	assertLiveReply(t, user.call(t, http.MethodPost, configRPC+"TestLLM", request, http.StatusOK))
	t.Log("restart the product with the same deployment credentials and retry from a new client")
	p.crash(t)
	p = w.startMode(t, true)
	assertLiveReply(t, p.user(t, "after-restart").call(t, http.MethodPost, configRPC+"TestLLM", request, http.StatusOK))
}

func TestLiveLLMConcurrentClients(t *testing.T) {
	request := liveLLMRequest(t)
	w := newWorkspace(t)
	p := w.startMode(t, true)
	first, second, observer := p.user(t, "first"), p.user(t, "second"), p.user(t, "observer")
	type outcome struct {
		response map[string]any
		status   int
		err      error
	}
	results := make(chan outcome, 2)
	start := make(chan struct{})
	var pending sync.WaitGroup
	for _, client := range []*userClient{first, second} {
		pending.Add(1)
		go func(client *userClient) {
			defer pending.Done()
			<-start
			response, status, err := client.request(http.MethodPost, configRPC+"TestLLM", request)
			results <- outcome{response, status, err}
		}(client)
	}
	close(start)
	// Even a failed foreground assertion must drain the users' pending requests.
	t.Cleanup(pending.Wait)
	for i := 0; i < 3; i++ {
		observer.config(t)
		status := observer.call(t, http.MethodPost, "/aiscan.rpc.system.SystemService/GetStatus", map[string]any{}, http.StatusOK)
		assertField(t, status, true, "status", "llmAvailable")
	}
	pending.Wait()
	close(results)
	for result := range results {
		if result.err != nil || result.status != http.StatusOK {
			t.Fatalf("concurrent live request: status=%d err=%v", result.status, result.err)
		}
		assertLiveReply(t, result.response)
	}
	observer.call(t, http.MethodPost, "/api/auth/logout", map[string]any{}, http.StatusOK)
	observer.call(t, http.MethodPost, "/api/auth/login", map[string]any{"token": "harness-local-access"}, http.StatusOK)
	assertLiveReply(t, observer.call(t, http.MethodPost, configRPC+"TestLLM", request, http.StatusOK))
}
