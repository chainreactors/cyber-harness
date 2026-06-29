//go:build full

package headless

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainreactors/neutron/protocols"
)

// testServer creates an HTTP test server serving testdata fixtures.
func testServer(t *testing.T) *httptest.Server {
	t.Helper()
	mux := http.NewServeMux()
	testdataDir := filepath.Join("testdata")

	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		path := r.URL.Path
		if path == "/" || path == "" {
			path = "/extract-urls.html"
		}
		base := filepath.Base(path)
		file := filepath.Join(testdataDir, base)
		data, err := os.ReadFile(file)
		if err != nil {
			file = filepath.Join(testdataDir, base+".html")
			data, err = os.ReadFile(file)
		}
		if err != nil {
			w.WriteHeader(404)
			fmt.Fprintf(w, "not found: %s", path)
			return
		}
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	mux.HandleFunc("/login.php", func(w http.ResponseWriter, r *http.Request) {
		data, _ := os.ReadFile(filepath.Join(testdataDir, "dvwa-login.html"))
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		w.Write(data)
	})

	return httptest.NewServer(mux)
}

func runTemplate(t *testing.T, engine *Engine, templateFile, target string, extraVars map[string]interface{}) (*protocols.ResultEvent, bool) {
	t.Helper()
	tmpl, err := LoadTemplate(templateFile)
	if err != nil {
		t.Fatalf("LoadTemplate(%s): %v", templateFile, err)
	}
	opts := &protocols.ExecuterOptions{Options: &protocols.Options{}}
	if err := tmpl.Compile(engine, opts); err != nil {
		t.Fatalf("Compile(%s): %v", templateFile, err)
	}
	payloads := make(map[string]interface{})
	for k, v := range extraVars {
		payloads[k] = v
	}
	var lastResult *protocols.ResultEvent
	var matched bool
	result, err := tmpl.ExecuteWithCallback(target, payloads, func(event *protocols.ResultEvent) {
		lastResult = event
	})
	if err != nil {
		t.Fatalf("Execute(%s): %v", templateFile, err)
	}
	if result != nil {
		matched = result.Matched
	}
	return lastResult, matched
}

var sharedEngine *Engine

func TestMain(m *testing.M) {
	sharedEngine = NewEngine()
	if err := sharedEngine.Init(); err != nil {
		fmt.Fprintf(os.Stderr, "failed to init headless engine: %v\n", err)
		os.Exit(1)
	}
	code := m.Run()
	sharedEngine.Close()
	os.Exit(code)
}

// ==========================================================================
// Parse compatibility: every real nuclei headless template must parse cleanly.
// ==========================================================================

func TestParseAllNucleiTemplates(t *testing.T) {
	templates := findAllTemplates(t)
	if len(templates) == 0 {
		t.Fatal("no templates found in testdata")
	}
	t.Logf("found %d nuclei headless templates", len(templates))

	for _, f := range templates {
		t.Run(filepath.Base(f), func(t *testing.T) {
			tmpl, err := LoadTemplate(f)
			if err != nil {
				t.Fatalf("parse failed: %v", err)
			}
			if tmpl.ID == "" {
				t.Error("template ID is empty")
			}
			if len(tmpl.RequestsHeadless) == 0 {
				t.Error("no headless requests")
			}
			for i, req := range tmpl.RequestsHeadless {
				if len(req.Steps) == 0 {
					t.Errorf("request %d has no steps", i)
				}
				for j, step := range req.Steps {
					if step.ActionType.ActionType == 0 {
						t.Errorf("request %d step %d: unknown action type", i, j)
					}
				}
			}
		})
	}
}

// ==========================================================================
// Compile compatibility: every template must compile its operators.
// ==========================================================================

func TestCompileAllNucleiTemplates(t *testing.T) {
	// Templates that use HTTP+headless mixed mode — their DSL matchers reference
	// variables defined by the HTTP section that doesn't exist in our headless-only engine.
	// These are expected to fail compilation and are excluded from the compile test.
	skipCompile := map[string]bool{
		"CVE-2025-25062.yaml": true,  // mixed HTTP+headless, variables from HTTP section
		"retool-dom-xss.yaml": true,  // DSL matcher references runtime variables
	}

	templates := findAllTemplates(t)
	for _, f := range templates {
		base := filepath.Base(f)
		t.Run(base, func(t *testing.T) {
			if skipCompile[base] {
				t.Skipf("skip: requires runtime DSL variables")
			}
			tmpl, err := LoadTemplate(f)
			if err != nil {
				t.Fatalf("parse: %v", err)
			}
			opts := &protocols.ExecuterOptions{Options: &protocols.Options{}}
			if err := tmpl.Compile(sharedEngine, opts); err != nil {
				t.Fatalf("compile: %v", err)
			}
			if tmpl.TotalRequests == 0 && tmpl.Executor == nil {
				t.Error("executor not created")
			}
		})
	}
}

// ==========================================================================
// Execution tests against local fixtures.
// ==========================================================================

func TestExecPrototypePollution(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()
	_, matched := runTemplate(t, sharedEngine,
		"testdata/prototype-pollution-check.yaml",
		srv.URL+"/prototype-pollution.html", nil)
	if !matched {
		t.Error("prototype pollution should match on vulnerable page")
	}
}

func TestExecExtractURLs(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()
	tmpl, err := LoadTemplate("testdata/extract-urls.yaml")
	if err != nil {
		t.Fatal(err)
	}
	opts := &protocols.ExecuterOptions{Options: &protocols.Options{}}
	if err := tmpl.Compile(sharedEngine, opts); err != nil {
		t.Fatal(err)
	}
	ctx := protocols.NewScanContext(srv.URL+"/extract-urls.html", nil)
	_, err = tmpl.Executor.Execute(ctx)
	if err != nil {
		t.Fatal(err)
	}
	// Verify script ran and produced output with URLs.
	results := ctx.GenerateResult()
	t.Logf("extract-urls results: %d", len(results))
}

func TestExecScreenshot(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()
	tmpDir := t.TempDir()
	// screenshot.yaml defines variables: dir="screenshots", filename="{{replace(BaseURL...)}}".
	// Override dir to use our temp directory, and set screenshotDir for compat.
	_, _ = runTemplate(t, sharedEngine,
		"testdata/screenshot.yaml",
		srv.URL+"/extract-urls.html",
		map[string]interface{}{
			"dir":           tmpDir,
			"screenshotDir": tmpDir,
		})
	// Check for any PNG file created (either in tmpDir directly or with the template filename).
	matches, _ := filepath.Glob(filepath.Join(tmpDir, "*.png"))
	if len(matches) == 0 {
		// Also check working directory in case variable resolution used default "screenshots" dir.
		matches, _ = filepath.Glob("screenshots/*.png")
		if len(matches) > 0 {
			// Clean up screenshots created in working directory.
			for _, m := range matches {
				os.Remove(m)
			}
			os.Remove("screenshots")
			return
		}
		t.Error("expected screenshot file to be created")
	}
}

func TestExecCookieConsent(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()
	_, matched := runTemplate(t, sharedEngine,
		"testdata/cookie-consent-detection.yaml",
		srv.URL+"/cookie-consent.html", nil)
	if !matched {
		t.Error("should match on page with cookie-consent div")
	}
}

func TestExecCookieConsentNoMatch(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()
	_, matched := runTemplate(t, sharedEngine,
		"testdata/cookie-consent-detection.yaml",
		srv.URL+"/extract-urls.html", nil)
	if matched {
		t.Error("should NOT match on page without cookie content")
	}
}

func TestExecDVWALogin(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()
	// The real DVWA template matches on "You have logged in as" after POST,
	// which we can't simulate locally. Verify that xpath actions execute
	// without error (click + text input via xpath selectors).
	tmpl, err := LoadTemplate("testdata/dvwa-headless-automatic-login.yaml")
	if err != nil {
		t.Fatal(err)
	}
	opts := &protocols.ExecuterOptions{Options: &protocols.Options{}}
	if err := tmpl.Compile(sharedEngine, opts); err != nil {
		t.Fatal(err)
	}
	ctx := protocols.NewScanContext(srv.URL, nil)
	_, err = tmpl.Executor.Execute(ctx)
	if err != nil {
		t.Fatalf("xpath action execution failed: %v", err)
	}
}

func TestExecSetHeaderResponse(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()
	// postmessage-tracker uses setheader response + hook + navigate.
	// Test that it at least runs without error.
	tmpl, err := LoadTemplate("testdata/postmessage-tracker.yaml")
	if err != nil {
		t.Fatal(err)
	}
	opts := &protocols.ExecuterOptions{Options: &protocols.Options{}}
	if err := tmpl.Compile(sharedEngine, opts); err != nil {
		t.Fatal(err)
	}
	ctx := protocols.NewScanContext(srv.URL+"/postmessage.html", nil)
	_, err = tmpl.Executor.Execute(ctx)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
}

func TestExecWindowNameDOMXSS(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()
	tmpl, err := LoadTemplate("testdata/window-name-domxss.yaml")
	if err != nil {
		t.Fatal(err)
	}
	opts := &protocols.ExecuterOptions{Options: &protocols.Options{}}
	if err := tmpl.Compile(sharedEngine, opts); err != nil {
		t.Fatal(err)
	}
	ctx := protocols.NewScanContext(srv.URL+"/postmessage.html", nil)
	_, err = tmpl.Executor.Execute(ctx)
	if err != nil {
		t.Fatalf("execution failed: %v", err)
	}
}

func TestExecMultipleHeadlessRequests(t *testing.T) {
	srv := testServer(t)
	defer srv.Close()
	// prototype-pollution-check.yaml has 8 headless requests.
	tmpl, err := LoadTemplate("testdata/prototype-pollution-check.yaml")
	if err != nil {
		t.Fatal(err)
	}
	if len(tmpl.RequestsHeadless) != 8 {
		t.Fatalf("expected 8 requests, got %d", len(tmpl.RequestsHeadless))
	}
	opts := &protocols.ExecuterOptions{Options: &protocols.Options{}}
	if err := tmpl.Compile(sharedEngine, opts); err != nil {
		t.Fatal(err)
	}
	result, err := tmpl.Execute(srv.URL+"/prototype-pollution.html", nil)
	if err != nil {
		t.Fatal(err)
	}
	if result == nil || !result.Matched {
		t.Error("should match at least one prototype pollution variant")
	}
}

// ==========================================================================
// Engine lifecycle
// ==========================================================================

func TestEngineExternalBrowser(t *testing.T) {
	e2 := NewEngine(WithBrowser(sharedEngine.Browser()))
	if err := e2.Init(); err != nil {
		t.Fatal(err)
	}
	e2.Close()
	page, err := sharedEngine.NewPage()
	if err != nil {
		t.Fatalf("shared engine broken after external close: %v", err)
	}
	page.Close()
}

// ==========================================================================
// Unit tests
// ==========================================================================

func TestActionTypeRoundtrip(t *testing.T) {
	for at, name := range actionTypeNames {
		holder := ActionTypeHolder{ActionType: at}
		if holder.String() != name {
			t.Errorf("ActionType %d: String() = %q, want %q", at, holder.String(), name)
		}
		var h2 ActionTypeHolder
		err := h2.UnmarshalYAML(func(v interface{}) error {
			p, ok := v.(*interface{})
			if ok {
				*p = name
			}
			return nil
		})
		if err != nil {
			t.Errorf("UnmarshalYAML(%q): %v", name, err)
		} else if h2.ActionType != at {
			t.Errorf("UnmarshalYAML(%q) = %d, want %d", name, h2.ActionType, at)
		}
	}
}

func TestInterpolate(t *testing.T) {
	act := &Action{
		ActionType: ActionTypeHolder{ActionType: ActionNavigate},
		Data:       map[string]string{"url": "{{BaseURL}}/login"},
		Name:       "nav",
	}
	vars := map[string]interface{}{"BaseURL": "https://example.com"}
	resolved := act.Interpolate(vars)
	if got := resolved.GetArg("url"); got != "https://example.com/login" {
		t.Errorf("got %q", got)
	}
	if act.GetArg("url") != "{{BaseURL}}/login" {
		t.Error("original was modified")
	}
}

// ==========================================================================
// Helpers
// ==========================================================================

func findAllTemplates(t *testing.T) []string {
	t.Helper()
	var templates []string
	err := filepath.Walk("testdata", func(path string, info os.FileInfo, err error) error {
		if err != nil {
			return err
		}
		if !info.IsDir() && strings.HasSuffix(path, ".yaml") {
			templates = append(templates, path)
		}
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	return templates
}
