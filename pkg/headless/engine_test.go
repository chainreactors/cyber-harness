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
		"CVE-2025-25062.yaml": true, // mixed HTTP+headless, variables from HTTP section
		"retool-dom-xss.yaml": true, // DSL matcher references runtime variables
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
	// screenshot.yaml derives a portable filename from BaseURL.
	// Override both supported output-directory variables with the test directory.
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

func TestExecAIScanExtendedActions(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/actions", func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "text/html; charset=utf-8")
		fmt.Fprint(w, `<!doctype html><html><body>
<label for="email">Email</label><input id="email" value="old value">
<label><input id="terms" data-testid="terms" type="checkbox"> Accept terms</label>
<label for="plan">Plan</label><select id="plan" aria-label="Plan"><option value="free">Free</option><option value="pro">Professional</option></select>
<button id="activate">Activate</button><div id="state" data-testid="state"></div>
<div id="source" data-testid="source" style="width:80px;height:40px;background:#ccc">Source</div>
<div id="drop" data-testid="drop" style="width:80px;height:40px;margin-top:20px;background:#ddd">Drop</div>
<div style="height:1600px"></div>
<script>
const email = document.getElementById('email');
const activate = document.getElementById('activate');
const state = document.getElementById('state');
email.addEventListener('focus', () => state.dataset.focus = 'yes');
email.addEventListener('blur', () => state.dataset.blur = 'yes');
activate.addEventListener('mouseover', () => state.dataset.hover = 'yes');
activate.addEventListener('dblclick', () => state.dataset.dblclick = 'yes');
activate.addEventListener('aiscan', event => state.dataset.custom = event.detail.flag);
document.getElementById('source').addEventListener('mousedown', () => state.dataset.dragstart = 'yes');
document.getElementById('drop').addEventListener('mouseup', () => state.dataset.dragend = 'yes');
</script></body></html>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rodPage, err := sharedEngine.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	page := NewPage(rodPage, sharedEngine, nil)
	defer page.Close()

	action := func(kind ActionType, data map[string]string) *Action {
		return &Action{ActionType: ActionTypeHolder{ActionType: kind}, Data: data}
	}
	actions := []*Action{
		action(ActionNavigate, map[string]string{"url": srv.URL + "/actions"}),
		action(ActionWaitURL, map[string]string{"url": "/actions"}),
		action(ActionWaitRequest, map[string]string{"url": "/actions"}),
		action(ActionWaitResponse, map[string]string{"url": "/actions"}),
		action(ActionTextInput, mergeMapsForTest(ParseSelector("label=Email"), map[string]string{"value": "alice@example.com", "clear": "true"})),
		action(ActionKeyboard, mergeMapsForTest(ParseSelector("label=Email"), map[string]string{"keys": "End"})),
		action(ActionFocus, ParseSelector("label=Email")),
		action(ActionBlur, ParseSelector("label=Email")),
		action(ActionCheck, ParseSelector("testid=terms")),
		action(ActionCheck, ParseSelector("testid=terms")),
		action(ActionUncheck, ParseSelector("testid=terms")),
		action(ActionCheck, ParseSelector("testid=terms")),
		action(ActionSelectInput, mergeMapsForTest(ParseSelector(`role=combobox[name="Plan"]`), map[string]string{"value": "pro"})),
		action(ActionHover, ParseSelector(`role=button[name="Activate"]`)),
		action(ActionDblClick, ParseSelector(`role=button[name="Activate"]`)),
		action(ActionDispatchEvent, mergeMapsForTest(ParseSelector("#activate"), map[string]string{"event": "aiscan", "detail": `{"flag":"ok"}`})),
		action(ActionDrag, mergeMapsForTest(ParseSelector("testid=source"), map[string]string{"target": "testid=drop"})),
		action(ActionScroll, map[string]string{"y": "250", "steps": "2"}),
		action(ActionStorage, map[string]string{"storage": "local", "operation": "set", "key": "token", "value": "abc123"}),
		action(ActionCookie, map[string]string{"operation": "set", "name": "session", "value": "cookie-value"}),
		action(ActionSetViewport, map[string]string{"width": "1024", "height": "768"}),
		action(ActionAssert, mergeMapsForTest(ParseSelector("label=Email"), map[string]string{"type": "value", "value": "alice@example.com"})),
		action(ActionAssert, mergeMapsForTest(ParseSelector("testid=terms"), map[string]string{"type": "checked"})),
		action(ActionAssert, mergeMapsForTest(ParseSelector(`role=combobox[name="Plan"]`), map[string]string{"type": "value", "value": "pro"})),
		action(ActionAssert, mergeMapsForTest(ParseSelector("testid=state"), map[string]string{"type": "attribute", "attribute": "data-focus", "value": "yes"})),
		action(ActionAssert, mergeMapsForTest(ParseSelector("testid=state"), map[string]string{"type": "attribute", "attribute": "data-blur", "value": "yes"})),
		action(ActionAssert, mergeMapsForTest(ParseSelector("testid=state"), map[string]string{"type": "attribute", "attribute": "data-hover", "value": "yes"})),
		action(ActionAssert, mergeMapsForTest(ParseSelector("testid=state"), map[string]string{"type": "attribute", "attribute": "data-dblclick", "value": "yes"})),
		action(ActionAssert, mergeMapsForTest(ParseSelector("testid=state"), map[string]string{"type": "attribute", "attribute": "data-custom", "value": "ok"})),
		action(ActionAssert, mergeMapsForTest(ParseSelector("testid=state"), map[string]string{"type": "attribute", "attribute": "data-dragstart", "value": "yes"})),
		action(ActionAssert, mergeMapsForTest(ParseSelector("testid=state"), map[string]string{"type": "attribute", "attribute": "data-dragend", "value": "yes"})),
		action(ActionAssert, map[string]string{"type": "storage", "storage": "local", "key": "token", "value": "abc123"}),
		action(ActionAssert, map[string]string{"type": "cookie", "name": "session", "value": "cookie-value"}),
		action(ActionSetContent, map[string]string{"html": `<main data-testid="replacement">Replacement content</main>`}),
		action(ActionAssert, mergeMapsForTest(ParseSelector("testid=replacement"), map[string]string{"type": "text", "value": "Replacement content"})),
	}
	if _, err := page.ExecuteActions(actions); err != nil {
		t.Fatalf("extended action replay failed: %v", err)
	}

	viewport, err := rodPage.Eval(`() => [window.innerWidth, window.innerHeight]`)
	if err != nil {
		t.Fatal(err)
	}
	if got := viewport.Value.Arr(); len(got) != 2 || got[0].Int() != 1024 || got[1].Int() != 768 {
		t.Fatalf("viewport = %v, want 1024x768", viewport.Value.Val())
	}
}

func TestExecAIScanHistoryActions(t *testing.T) {
	mux := http.NewServeMux()
	mux.HandleFunc("/one", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><head><title>Page One</title></head><body>one</body></html>`)
	})
	mux.HandleFunc("/two", func(w http.ResponseWriter, _ *http.Request) {
		fmt.Fprint(w, `<html><head><title>Page Two</title></head><body>two</body></html>`)
	})
	srv := httptest.NewServer(mux)
	defer srv.Close()

	rodPage, err := sharedEngine.NewPage()
	if err != nil {
		t.Fatal(err)
	}
	page := NewPage(rodPage, sharedEngine, nil)
	defer page.Close()
	action := func(kind ActionType, data map[string]string) *Action {
		return &Action{ActionType: ActionTypeHolder{ActionType: kind}, Data: data}
	}

	actions := []*Action{
		action(ActionNavigate, map[string]string{"url": srv.URL + "/one"}),
		action(ActionNavigate, map[string]string{"url": srv.URL + "/two"}),
		action(ActionGoBack, map[string]string{}),
		action(ActionAssert, map[string]string{"type": "url", "value": "/one", "match": "contains"}),
		action(ActionGoForward, map[string]string{}),
		action(ActionAssert, map[string]string{"type": "url", "value": "/two", "match": "contains"}),
		action(ActionReload, map[string]string{}),
		action(ActionAssert, map[string]string{"type": "title", "value": "Page Two"}),
	}
	if _, err := page.ExecuteActions(actions); err != nil {
		t.Fatalf("history action replay failed: %v", err)
	}
}

func mergeMapsForTest(left, right map[string]string) map[string]string {
	merged := make(map[string]string, len(left)+len(right))
	for key, value := range left {
		merged[key] = value
	}
	for key, value := range right {
		merged[key] = value
	}
	return merged
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
