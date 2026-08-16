//go:build full

package scan

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	browserutil "github.com/chainreactors/aiscan/pkg/browser"
	katanaoutput "github.com/projectdiscovery/katana/pkg/output"
)

func TestKatanaProfileExtender(t *testing.T) {
	quick, err := profileForMode("quick")
	if err != nil {
		t.Fatalf("quick profile error: %v", err)
	}
	if !quick.Enabled(capKatanaCrawl) {
		t.Fatal("quick profile should enable katana_crawl")
	}
	if quick.Enabled(capKatanaDeep) {
		t.Fatal("quick profile should not enable katana_deep")
	}

	full, err := profileForMode("full")
	if err != nil {
		t.Fatalf("full profile error: %v", err)
	}
	if !full.Enabled(capKatanaCrawl) {
		t.Fatal("full profile should enable katana_crawl")
	}
	if !full.Enabled(capKatanaDeep) {
		t.Fatal("full profile should enable katana_deep")
	}
}

func TestRunKatanaCrawlEmitsTargets(t *testing.T) {
	cmd := &Command{}
	wt := newWebTarget("", "https://www.example.com", "")
	e := targetEvent(capSprayCheck, "", wt)

	var emitted []event
	emit := func(ev event) {
		emitted = append(emitted, ev)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()

	runKatanaCrawl(ctx, cmd, e, 1, false, emit)

	targets := 0
	for _, ev := range emitted {
		if ev.Kind == eventTarget {
			targets++
			if wTarget, ok := ev.Target.(webTarget); ok {
				t.Logf("  discovered: %s", wTarget.URL)
			}
		}
	}
	t.Logf("katana discovered %d web targets from example.com (depth=1)", targets)
}

func TestRunKatanaCrawlHonorsContextCancellation(t *testing.T) {
	requestStarted := make(chan struct{}, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) {
		select {
		case requestStarted <- struct{}{}:
		default:
		}
		<-r.Context().Done()
	}))
	defer srv.Close()

	cmd := &Command{}
	e := targetEvent(capSprayCheck, srv.URL, newWebTarget(srv.URL, srv.URL, ""))
	ctx, cancel := context.WithCancel(context.Background())
	done := make(chan struct{})
	go func() {
		runKatanaCrawl(ctx, cmd, e, 1, false, func(event) {})
		close(done)
	}()

	select {
	case <-requestStarted:
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("Katana capability did not start the test request")
	}
	cancel()

	select {
	case <-done:
	case <-time.After(3 * time.Second):
		t.Fatal("Katana capability did not stop after context cancellation")
	}
}

func TestScanResultWriterGetResultCount(t *testing.T) {
	writer := &scanResultWriter{}
	const writes = 32
	done := make(chan struct{}, writes)
	for range writes {
		go func() {
			if err := writer.Write(&katanaoutput.Result{}); err != nil {
				t.Errorf("Write() error = %v", err)
			}
			done <- struct{}{}
		}()
	}
	for range writes {
		<-done
	}
	if got := writer.GetResultCount(); got != writes {
		t.Fatalf("GetResultCount() = %d, want %d", got, writes)
	}
}

func TestE2EKatanaDeepRendersAuthenticatedSPA(t *testing.T) {
	binary, err := browserutil.Discover()
	if err != nil {
		t.Fatalf("discover browser: %v", err)
	}
	if binary.Path == "" {
		t.Skip("no system browser available; CI installs Chrome for this test")
	}
	t.Setenv(browserutil.PathEnv, binary.Path)

	const (
		sessionToken  = "scan-session-73"
		workspacePath = "/workspace/session-73?view=assets"
	)
	var authenticatedWorkspaceHits atomic.Int32

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/" {
			http.NotFound(w, r)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><body><main>Loading assets...</main><script>
(async () => {
  const response = await fetch('/api/session', {
    method: 'POST',
    headers: {'Content-Type': 'application/json', 'X-CSRF-Token': 'scan-e2e'},
    body: JSON.stringify({username: 'analyst'})
  });
  const session = await response.json();
  localStorage.setItem('scan.token', session.token);
  document.cookie = 'scan_session=' + session.token + '; Path=/; SameSite=Lax';
  location.assign(session.next);
})();
</script></body></html>`)
	})
	mux.HandleFunc("/api/session", func(w http.ResponseWriter, r *http.Request) {
		if r.Method != http.MethodPost || r.Header.Get("X-CSRF-Token") != "scan-e2e" {
			http.Error(w, "invalid session request", http.StatusForbidden)
			return
		}
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprintf(w, `{"token":%q,"next":%q}`, sessionToken, workspacePath)
	})
	mux.HandleFunc("/workspace/session-73", func(w http.ResponseWriter, r *http.Request) {
		cookie, err := r.Cookie("scan_session")
		if err != nil || cookie.Value != sessionToken {
			http.Error(w, "authentication required", http.StatusUnauthorized)
			return
		}
		authenticatedWorkspaceHits.Add(1)
		fmt.Fprint(w, `<!doctype html><html><body><a href="/assets/detail?id=9">Asset detail</a></body></html>`)
	})
	mux.HandleFunc("/assets/detail", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, "asset detail")
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	cmd := &Command{}
	e := targetEvent(capSprayCheck, srv.URL, newWebTarget(srv.URL, srv.URL, ""))
	var emitted []event
	ctx, cancel := context.WithTimeout(context.Background(), 75*time.Second)
	defer cancel()
	runKatanaCrawl(ctx, cmd, e, 2, true, func(ev event) {
		emitted = append(emitted, ev)
	})

	for _, ev := range emitted {
		if ev.Kind == eventError {
			t.Fatalf("katana_deep emitted error: %s", ev.Error.Message)
		}
	}
	if authenticatedWorkspaceHits.Load() == 0 {
		t.Fatal("katana_deep browser never reached the authenticated workspace")
	}
	for _, ev := range emitted {
		if ev.Kind != eventTarget {
			continue
		}
		wt, ok := ev.Target.(webTarget)
		if ok && strings.Contains(wt.URL, "/workspace/session-73") {
			return
		}
	}
	t.Fatal("katana_deep did not emit the browser-only workspace route")
}
