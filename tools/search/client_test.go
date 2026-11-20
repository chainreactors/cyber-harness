package search

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"slices"
	"strings"
	"sync"
	"testing"
)

func TestClientRotatesKeysAndRetainsWorkingKey(t *testing.T) {
	var mu sync.Mutex
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		key := strings.TrimPrefix(r.Header.Get("Authorization"), "Bearer ")
		mu.Lock()
		keys = append(keys, key)
		mu.Unlock()
		if r.URL.Path != "/gateway/search" {
			t.Errorf("key rotation changed upstream path: %s", r.URL.Path)
		}
		if key == "expired" {
			w.WriteHeader(http.StatusUnauthorized)
			return
		}
		fmt.Fprint(w, `{"results":[{"title":"Fixture","url":"https://example.test"}]}`)
	}))
	defer server.Close()
	client := New(Config{Keys: " expired, working, expired, ", BaseURL: server.URL + "/gateway"})
	for range 2 {
		result, err := client.Execute(t.Context(), []string{"fixture"})
		if err != nil || !strings.Contains(result, "https://example.test") {
			t.Fatalf("search failed during rotation: result=%s err=%v", result, err)
		}
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(keys, []string{"expired", "working", "working"}) {
		t.Fatalf("incorrect key rotation or deduplication: %v", keys)
	}
}

func TestClientStopsAfterTryingAllKeys(t *testing.T) {
	var mu sync.Mutex
	var keys []string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		keys = append(keys, r.Header.Get("Authorization"))
		mu.Unlock()
		w.WriteHeader(http.StatusForbidden)
	}))
	defer server.Close()
	client := New(Config{Keys: "first,second", BaseURL: server.URL})
	if _, err := client.Execute(t.Context(), []string{"fixture"}); err == nil || !strings.Contains(err.Error(), "403") {
		t.Fatalf("exhausted upstream keys did not return their error: %v", err)
	}
	mu.Lock()
	defer mu.Unlock()
	if !slices.Equal(keys, []string{"Bearer first", "Bearer second"}) {
		t.Fatalf("key cycle did not stop: %v", keys)
	}
}

func TestParseTavilyArgsBasicQuery(t *testing.T) {
	query, num, err := parseTavilyArgs([]string{"CVE-2024-1234", "exploit"})
	if err != nil {
		t.Fatalf("parseTavilyArgs() error = %v", err)
	}
	if query != "CVE-2024-1234 exploit" {
		t.Fatalf("query = %q, want %q", query, "CVE-2024-1234 exploit")
	}
	if num != defaultNumResults {
		t.Fatalf("num = %d, want %d", num, defaultNumResults)
	}
}

func TestClientUsesInjectedKeysOnly(t *testing.T) {
	t.Setenv("TAVILY_API_KEY", "ambient-key")
	search := New(Config{Keys: "resolved-key"})
	_, key := search.currentAPIKey()
	if key != "resolved-key" || len(search.apiKeys) != 1 {
		t.Fatalf("ambient environment bypassed resolved config: %#v", search.apiKeys)
	}
}

func TestParseTavilyArgsWithNum(t *testing.T) {
	query, num, err := parseTavilyArgs([]string{"nginx", "--num", "8"})
	if err != nil {
		t.Fatalf("parseTavilyArgs() error = %v", err)
	}
	if query != "nginx" {
		t.Fatalf("query = %q", query)
	}
	if num != 8 {
		t.Fatalf("num = %d, want 8", num)
	}
}

func TestParseTavilyArgsClampsNum(t *testing.T) {
	_, num, err := parseTavilyArgs([]string{"test", "--num", "999"})
	if err != nil {
		t.Fatal(err)
	}
	if num != maxNumResults {
		t.Fatalf("num = %d, want clamped to %d", num, maxNumResults)
	}

	_, num, err = parseTavilyArgs([]string{"test", "--num", "0"})
	if err != nil {
		t.Fatal(err)
	}
	if num != defaultNumResults {
		t.Fatalf("num = %d, want default %d", num, defaultNumResults)
	}
}

func TestParseTavilyArgsRequiresQuery(t *testing.T) {
	_, _, err := parseTavilyArgs([]string{})
	if err == nil {
		t.Fatal("expected error for empty args")
	}
	if !strings.Contains(err.Error(), "required") {
		t.Fatalf("error = %q", err)
	}
}

func TestParseTavilyArgsRejectsUnknownFlag(t *testing.T) {
	_, _, err := parseTavilyArgs([]string{"test", "--bad"})
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
}
