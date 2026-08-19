package katana

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	browserutil "github.com/chainreactors/aiscan/pkg/browser"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/projectdiscovery/katana/pkg/navigation"
	katanaoutput "github.com/projectdiscovery/katana/pkg/output"
	katanatypes "github.com/projectdiscovery/katana/pkg/types"
)

func TestConfigureBrowserOptionsPriority(t *testing.T) {
	tests := []struct {
		name          string
		options       katanatypes.Options
		discovered    browserutil.Binary
		discoveryErr  error
		wantPath      string
		wantInstalled bool
		wantCalls     int
		wantErr       bool
	}{
		{
			name:      "CDP endpoint takes precedence",
			options:   katanatypes.Options{Headless: true, ChromeWSUrl: "ws://127.0.0.1:9222/devtools/browser/test"},
			wantCalls: 0,
		},
		{
			name:          "explicit Katana path takes precedence",
			options:       katanatypes.Options{Headless: true, SystemChromePath: "/explicit/chrome"},
			wantPath:      "/explicit/chrome",
			wantInstalled: true,
			wantCalls:     0,
		},
		{
			name:          "headless uses shared discovery",
			options:       katanatypes.Options{Headless: true},
			discovered:    browserutil.Binary{Path: "/system/chromium", Source: browserutil.SourceSystem},
			wantPath:      "/system/chromium",
			wantInstalled: true,
			wantCalls:     1,
		},
		{
			name:          "hybrid uses shared discovery",
			options:       katanatypes.Options{HeadlessHybrid: true},
			discovered:    browserutil.Binary{Path: "/system/edge", Source: browserutil.SourceSystem},
			wantPath:      "/system/edge",
			wantInstalled: true,
			wantCalls:     1,
		},
		{
			name:      "standard crawler does not need a browser",
			options:   katanatypes.Options{},
			wantCalls: 0,
		},
		{
			name:      "missing browser preserves Rod fallback",
			options:   katanatypes.Options{Headless: true},
			wantCalls: 1,
		},
		{
			name:         "discovery errors are returned",
			options:      katanatypes.Options{Headless: true},
			discoveryErr: errors.New("bad browser override"),
			wantCalls:    1,
			wantErr:      true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			calls := 0
			err := configureBrowserOptionsWith(&tt.options, func() (browserutil.Binary, error) {
				calls++
				return tt.discovered, tt.discoveryErr
			})
			if (err != nil) != tt.wantErr {
				t.Fatalf("configureBrowserOptionsWith error = %v, wantErr %v", err, tt.wantErr)
			}
			if calls != tt.wantCalls {
				t.Fatalf("discover calls = %d, want %d", calls, tt.wantCalls)
			}
			if tt.options.SystemChromePath != tt.wantPath {
				t.Fatalf("SystemChromePath = %q, want %q", tt.options.SystemChromePath, tt.wantPath)
			}
			if tt.options.UseInstalledChrome != tt.wantInstalled {
				t.Fatalf("UseInstalledChrome = %v, want %v", tt.options.UseInstalledChrome, tt.wantInstalled)
			}
		})
	}
}

func TestReadFlagsV17CrawlerOptions(t *testing.T) {
	options, err := readFlags([]string{
		"-u", "https://example.com",
		"--kb-secrets",
		"--kb-validate-secrets",
		"--kb-endpoints",
		"--page-content-similar",
		"--similarity-deduplication",
		"--page-content-similar-mode", "bm25",
		"--page-content-similar-distance", "7",
		"--page-content-similar-threshold", "0.72",
		"--page-content-similar-budget", "4",
	})
	if err != nil {
		t.Fatalf("readFlags() error = %v", err)
	}
	if !options.Secrets || !options.ValidateSecrets || !options.Endpoints {
		t.Fatalf("knowledge base options = (secrets=%v validate=%v endpoints=%v), want all true", options.Secrets, options.ValidateSecrets, options.Endpoints)
	}
	if !options.PageContentSimilar || !options.SimilarityDeduplication {
		t.Fatalf("similarity flags = (page=%v alias=%v), want both true", options.PageContentSimilar, options.SimilarityDeduplication)
	}
	if options.PageContentSimilarMode != "bm25" || options.PageContentSimilarDistance != 7 || options.PageContentSimilarThresholdStr != "0.72" || options.PageContentSimilarBudget != 4 {
		t.Fatalf("similarity options = (%q, %d, %q, %d), want (bm25, 7, 0.72, 4)", options.PageContentSimilarMode, options.PageContentSimilarDistance, options.PageContentSimilarThresholdStr, options.PageContentSimilarBudget)
	}

	defaults, err := readFlags([]string{"-u", "https://example.com"})
	if err != nil {
		t.Fatalf("readFlags(defaults) error = %v", err)
	}
	if defaults.PageContentSimilarMode != "simhash" || defaults.PageContentSimilarDistance != 3 || defaults.PageContentSimilarThresholdStr != "0.85" || defaults.PageContentSimilarBudget != 1 {
		t.Fatalf("similarity defaults = (%q, %d, %q, %d), want (simhash, 3, 0.85, 1)", defaults.PageContentSimilarMode, defaults.PageContentSimilarDistance, defaults.PageContentSimilarThresholdStr, defaults.PageContentSimilarBudget)
	}
}

func TestRunHonorsContextCancellation(t *testing.T) {
	requestStarted := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case requestStarted <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan error, 1)
	go func() {
		var output bytes.Buffer
		_, err := New().Run(ctx, &commands.Execution{
			Args:   []string{"-u", srv.URL, "-d", "1", "-timeout", "30"},
			Stdout: &output,
			Stderr: &output,
		})
		done <- err
	}()

	select {
	case <-requestStarted:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("Katana did not start the test request")
	}
	cancel()

	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("Katana error = %v, want context.Canceled", err)
		}
	case <-time.After(3 * time.Second):
		t.Fatal("Katana did not stop after context cancellation")
	}
}

func TestResultCollectorGetResultCount(t *testing.T) {
	collector := &resultCollector{}
	result := &katanaoutput.Result{Request: &navigation.Request{URL: "https://example.com/a"}}
	if err := collector.Write(result); err != nil {
		t.Fatalf("Write() error = %v", err)
	}
	if err := collector.Write(result); err != nil {
		t.Fatalf("duplicate Write() error = %v", err)
	}
	if got := collector.GetResultCount(); got != 1 {
		t.Fatalf("GetResultCount() = %d, want 1", got)
	}
}

func TestE2EHeadlessReusesDiscoveredBrowser(t *testing.T) {
	binary, err := browserutil.Discover()
	if err != nil {
		t.Fatalf("discover browser: %v", err)
	}
	if binary.Path == "" {
		t.Skip("no system browser available; CI installs Chrome for this test")
	}
	t.Setenv(browserutil.PathEnv, binary.Path)

	const (
		sessionToken  = "aiscan-session-42"
		workspacePath = "/workspace/session-42?view=issues"
	)
	var rootHits atomic.Int32
	var sessionHits atomic.Int32
	var authenticatedWorkspaceHits atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		rootHits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html>
<html><head><title>AIScan Workspace Login</title></head>
<body><main id="app">Signing in...</main>
<script>
(async () => {
  const response = await fetch('/api/session', {
    method: 'POST',
    headers: {'Content-Type': 'application/json', 'X-CSRF-Token': 'browser-e2e'},
    body: JSON.stringify({username: 'analyst', workspace: 'security'})
  });
  const session = await response.json();
  localStorage.setItem('aiscan.token', session.token);
  document.cookie = 'aiscan_session=' + session.token + '; Path=/; SameSite=Lax';
  location.assign(session.next);
})();
</script></body></html>`)
	})
	mux.HandleFunc("/api/session", func(w http.ResponseWriter, r *http.Request) {
		sessionHits.Add(1)
		if r.Method != http.MethodPost || r.Header.Get("X-CSRF-Token") != "browser-e2e" {
			http.Error(w, "invalid session request", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":%q,"next":%q}`, sessionToken, workspacePath)
	})
	mux.HandleFunc("/workspace/session-42", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("aiscan_session")
		if err != nil || cookie.Value != sessionToken {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		authenticatedWorkspaceHits.Add(1)
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><body><h1>Security issues</h1><a href="/issues/critical?id=7">Critical issue</a></body></html>`)
	})
	mux.HandleFunc("/issues/critical", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "confirmed issue")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	options, err := readFlags([]string{"-u", srv.URL, "-hl", "-d", "2"})
	if err != nil {
		t.Fatalf("parse headless options: %v", err)
	}
	if err := configureBrowserOptions(options); err != nil {
		t.Fatalf("configure browser options: %v", err)
	}
	if options.SystemChromePath != binary.Path || !options.UseInstalledChrome {
		t.Fatalf("Katana browser = (%q, installed=%v), want (%q, true)", options.SystemChromePath, options.UseInstalledChrome, binary.Path)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	var output bytes.Buffer
	_, err = New().Run(ctx, &commands.Execution{
		Args:   []string{"-u", srv.URL, "-hl", "-d", "2", "-timeout", "15", "-ct", "45s", "-j"},
		Stdout: &output,
		Stderr: &output,
	})
	if err != nil {
		t.Fatalf("Katana headless crawl failed: %v\noutput:\n%s", err, output.String())
	}
	if authenticatedWorkspaceHits.Load() == 0 {
		t.Fatalf("browser never reached the authenticated workspace (root=%d session=%d)\noutput:\n%s", rootHits.Load(), sessionHits.Load(), output.String())
	}
	if !strings.Contains(output.String(), "/workspace/session-42") {
		t.Fatalf("Katana output does not contain the browser-only workspace route\noutput:\n%s", output.String())
	}
}
