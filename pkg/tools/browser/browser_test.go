package browser

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/go-rod/rod/lib/launcher"
)

// ---------------------------------------------------------------------------
// Argument parsing unit tests (no browser needed)
// ---------------------------------------------------------------------------

func TestParseCommonOpts_URLRequired(t *testing.T) {
	_, err := parseCommonOpts(nil, true, "usage")
	if err == nil {
		t.Fatal("expected error for missing URL")
	}
	if !strings.Contains(err.Error(), "URL is required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseCommonOpts_DefaultTimeout(t *testing.T) {
	opts, err := parseCommonOpts([]string{"https://example.com"}, true, "usage")
	if err != nil {
		t.Fatal(err)
	}
	if opts.timeout != defaultTimeout {
		t.Fatalf("expected default timeout %v, got %v", defaultTimeout, opts.timeout)
	}
}

func TestParseCommonOpts_CustomTimeout(t *testing.T) {
	opts, err := parseCommonOpts([]string{"https://example.com", "--timeout", "60"}, true, "usage")
	if err != nil {
		t.Fatal(err)
	}
	if opts.timeout != 60*time.Second {
		t.Fatalf("expected 60s timeout, got %v", opts.timeout)
	}
}

func TestParseCommonOpts_UserAgent(t *testing.T) {
	opts, err := parseCommonOpts([]string{"https://example.com", "--user-agent", "MyBot/1.0"}, true, "usage")
	if err != nil {
		t.Fatal(err)
	}
	if opts.userAgent != "MyBot/1.0" {
		t.Fatalf("expected user-agent MyBot/1.0, got %q", opts.userAgent)
	}
}

func TestParseCommonOpts_UnknownFlag(t *testing.T) {
	_, err := parseCommonOpts([]string{"--bogus"}, true, "usage")
	if err == nil {
		t.Fatal("expected error for unknown flag")
	}
	if !strings.Contains(err.Error(), "unknown flag") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestParseCommonOpts_TimeoutMissingValue(t *testing.T) {
	_, err := parseCommonOpts([]string{"https://example.com", "--timeout"}, true, "usage")
	if err == nil {
		t.Fatal("expected error for --timeout without value")
	}
}

func TestParseScreenshotOpts_FullPage(t *testing.T) {
	opts, err := parseScreenshotOpts([]string{"https://example.com", "--full-page", "--output", "test.png"}, "usage")
	if err != nil {
		t.Fatal(err)
	}
	if !opts.fullPage {
		t.Fatal("expected fullPage to be true")
	}
	if opts.output != "test.png" {
		t.Fatalf("expected output test.png, got %q", opts.output)
	}
}

func TestParseEvalOpts_Positional(t *testing.T) {
	opts, err := parseEvalOpts([]string{"https://example.com", "document.title"}, "usage")
	if err != nil {
		t.Fatal(err)
	}
	if opts.url != "https://example.com" {
		t.Fatalf("expected url https://example.com, got %q", opts.url)
	}
	if opts.script != "document.title" {
		t.Fatalf("expected script 'document.title', got %q", opts.script)
	}
}

func TestParseEvalOpts_ScriptFlag(t *testing.T) {
	opts, err := parseEvalOpts([]string{"https://example.com", "--script", "1+1"}, "usage")
	if err != nil {
		t.Fatal(err)
	}
	if opts.script != "1+1" {
		t.Fatalf("expected script '1+1', got %q", opts.script)
	}
}

func TestParseEvalOpts_MissingScript(t *testing.T) {
	_, err := parseEvalOpts([]string{"https://example.com"}, "usage")
	if err == nil {
		t.Fatal("expected error for missing JS expression")
	}
}

func TestParsePDFOpts_Output(t *testing.T) {
	opts, err := parsePDFOpts([]string{"https://example.com", "--output", "report.pdf"}, "usage")
	if err != nil {
		t.Fatal(err)
	}
	if opts.output != "report.pdf" {
		t.Fatalf("expected output report.pdf, got %q", opts.output)
	}
}

func TestExecute_NoSubcommand(t *testing.T) {
	cmd := New(t.TempDir())
	_, err := cmd.Execute(context.Background(), nil)
	if err == nil {
		t.Fatal("expected error for no subcommand")
	}
	if !strings.Contains(err.Error(), "subcommand required") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestExecute_UnknownSubcommand(t *testing.T) {
	cmd := New(t.TempDir())
	_, err := cmd.Execute(context.Background(), []string{"bogus"})
	if err == nil {
		t.Fatal("expected error for unknown subcommand")
	}
	if !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestResolvePath_Absolute(t *testing.T) {
	got := resolvePath("/work", "/abs/path.png")
	if got != "/abs/path.png" {
		t.Fatalf("expected /abs/path.png, got %q", got)
	}
}

func TestResolvePath_Relative(t *testing.T) {
	got := resolvePath("/work", "file.png")
	if got != "/work/file.png" {
		t.Fatalf("expected /work/file.png, got %q", got)
	}
}

func TestNameAndUsage(t *testing.T) {
	cmd := New(t.TempDir())
	if cmd.Name() != "browser" {
		t.Fatalf("expected name 'browser', got %q", cmd.Name())
	}
	if !strings.Contains(cmd.Usage(), "navigate") {
		t.Fatal("Usage() should mention navigate subcommand")
	}
	if !strings.Contains(cmd.Usage(), "screenshot") {
		t.Fatal("Usage() should mention screenshot subcommand")
	}
}

func TestFormatTextOutput_Truncation(t *testing.T) {
	long := strings.Repeat("a", maxOutputLen+100)
	out := formatTextOutput("https://example.com", long)
	if !strings.Contains(out, "[Content truncated") {
		t.Fatal("expected truncation notice")
	}
}

func TestFormatNetworkOutput_Empty(t *testing.T) {
	out := formatNetworkOutput("https://example.com", nil)
	if !strings.Contains(out, "0 requests") {
		t.Fatal("expected 0 requests")
	}
	if !strings.Contains(out, "No network requests") {
		t.Fatal("expected no-requests message")
	}
}

// ---------------------------------------------------------------------------
// Integration tests (require Chromium)
// ---------------------------------------------------------------------------

func skipIfNoBrowser(t *testing.T) {
	t.Helper()
	if _, exists := launcher.LookPath(); !exists {
		t.Skip("no Chromium/Chrome found, skipping browser integration test")
	}
}

func newTestServer(handler http.HandlerFunc) *httptest.Server {
	return httptest.NewServer(handler)
}

func TestIntegration_Navigate(t *testing.T) {
	skipIfNoBrowser(t)

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html>
<html><body>
<div id="app"></div>
<script>document.getElementById('app').textContent = 'JS rendered content';</script>
</body></html>`)
	})
	defer srv.Close()

	cmd := New(t.TempDir())
	defer cmd.Close()

	out, err := cmd.Execute(context.Background(), []string{"navigate", srv.URL})
	if err != nil {
		t.Fatalf("navigate failed: %v", err)
	}
	if !strings.Contains(out, "JS rendered content") {
		t.Fatalf("expected JS-rendered content in output, got:\n%s", out)
	}
}

func TestIntegration_Screenshot(t *testing.T) {
	skipIfNoBrowser(t)

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body><h1>Hello</h1></body></html>`)
	})
	defer srv.Close()

	workDir := t.TempDir()
	cmd := New(workDir)
	defer cmd.Close()

	out, err := cmd.Execute(context.Background(), []string{
		"screenshot", srv.URL, "--output", "test.png",
	})
	if err != nil {
		t.Fatalf("screenshot failed: %v", err)
	}
	if !strings.Contains(out, "test.png") {
		t.Fatalf("expected output to mention test.png, got:\n%s", out)
	}

	path := filepath.Join(workDir, "test.png")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("screenshot file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("screenshot file is empty")
	}
}

func TestIntegration_Content(t *testing.T) {
	skipIfNoBrowser(t)

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body>
<script>document.body.innerHTML += '<p id="dynamic">injected</p>';</script>
</body></html>`)
	})
	defer srv.Close()

	cmd := New(t.TempDir())
	defer cmd.Close()

	out, err := cmd.Execute(context.Background(), []string{"content", srv.URL})
	if err != nil {
		t.Fatalf("content failed: %v", err)
	}
	if !strings.Contains(out, "dynamic") || !strings.Contains(out, "injected") {
		t.Fatalf("expected JS-injected element in HTML, got:\n%s", out)
	}
}

func TestIntegration_Eval(t *testing.T) {
	skipIfNoBrowser(t)

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><head><title>Test Page</title></head><body></body></html>`)
	})
	defer srv.Close()

	cmd := New(t.TempDir())
	defer cmd.Close()

	out, err := cmd.Execute(context.Background(), []string{"eval", srv.URL, "document.title"})
	if err != nil {
		t.Fatalf("eval failed: %v", err)
	}
	if !strings.Contains(out, "Test Page") {
		t.Fatalf("expected 'Test Page' in eval result, got:\n%s", out)
	}
}

func TestIntegration_Network(t *testing.T) {
	skipIfNoBrowser(t)

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/api/data" {
			w.Header().Set("Content-Type", "application/json")
			fmt.Fprint(w, `{"ok":true}`)
			return
		}
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprintf(w, `<!DOCTYPE html><html><body>
<script>fetch('%s/api/data').then(r=>r.json());</script>
</body></html>`, "")
	})
	defer srv.Close()

	cmd := New(t.TempDir())
	defer cmd.Close()

	out, err := cmd.Execute(context.Background(), []string{"network", srv.URL, "--timeout", "10"})
	if err != nil {
		t.Fatalf("network failed: %v", err)
	}
	// At minimum, the page itself should be captured.
	if !strings.Contains(out, "Captured:") {
		t.Fatalf("expected network capture output, got:\n%s", out)
	}
	if !strings.Contains(out, "/api/data") {
		t.Fatalf("expected fetch request in network output, got:\n%s", out)
	}
	if !strings.Contains(out, "application/json") {
		t.Fatalf("expected response content type in network output, got:\n%s", out)
	}
}

func TestIntegration_PDF(t *testing.T) {
	skipIfNoBrowser(t)

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body><h1>PDF Test</h1></body></html>`)
	})
	defer srv.Close()

	workDir := t.TempDir()
	cmd := New(workDir)
	defer cmd.Close()

	out, err := cmd.Execute(context.Background(), []string{
		"pdf", srv.URL, "--output", "test.pdf",
	})
	if err != nil {
		t.Fatalf("pdf failed: %v", err)
	}
	if !strings.Contains(out, "test.pdf") {
		t.Fatalf("expected output to mention test.pdf, got:\n%s", out)
	}

	path := filepath.Join(workDir, "test.pdf")
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("PDF file not found: %v", err)
	}
	if info.Size() == 0 {
		t.Fatal("PDF file is empty")
	}
}

func TestIntegration_BrowserReuse(t *testing.T) {
	skipIfNoBrowser(t)

	srv := newTestServer(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/html")
		fmt.Fprint(w, `<!DOCTYPE html><html><body>ok</body></html>`)
	})
	defer srv.Close()

	cmd := New(t.TempDir())
	defer cmd.Close()

	// First call launches browser.
	_, err := cmd.Execute(context.Background(), []string{"navigate", srv.URL})
	if err != nil {
		t.Fatalf("first navigate failed: %v", err)
	}

	// Second call should reuse the same browser.
	_, err = cmd.Execute(context.Background(), []string{"navigate", srv.URL})
	if err != nil {
		t.Fatalf("second navigate failed: %v", err)
	}
}
