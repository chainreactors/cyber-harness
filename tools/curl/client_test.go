package curl

import (
	"context"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

// run is a small harness: parse args, execute against a real server, capture
// stdout/stderr. It never routes through a proxy (env is empty), so it exercises
// the client and naturalization logic directly.
func run(t *testing.T, args []string, env, workDir string) (stdout, stderr string, err error) {
	t.Helper()
	req, perr := Parse(args)
	if perr != nil {
		return "", "", perr
	}
	var out, errb strings.Builder
	envMapVal := map[string]string{}
	if env != "" {
		k, v, _ := strings.Cut(env, "=")
		envMapVal[k] = v
	}
	c := New()
	err = c.do(context.Background(), req, envMapVal, workDir, &out, &errb)
	return out.String(), errb.String(), err
}

func TestGetWritesBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("hello"))
	}))
	defer srv.Close()

	out, _, err := run(t, []string{srv.URL}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello" {
		t.Fatalf("body = %q", out)
	}
}

func TestDefaultBrowserHeaders(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = r.Header.Clone()
	}))
	defer srv.Close()

	if _, _, err := run(t, []string{srv.URL}, "", ""); err != nil {
		t.Fatal(err)
	}
	if ua := got.Get("User-Agent"); !strings.Contains(ua, "Chrome/") {
		t.Fatalf("default UA not a browser: %q", ua)
	}
	for _, h := range []string{"Accept", "Accept-Language", "Sec-Fetch-Mode", "Upgrade-Insecure-Requests"} {
		if got.Get(h) == "" {
			t.Fatalf("missing default header %s", h)
		}
	}
}

func TestUserAgentOverride(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Header.Clone() }))
	defer srv.Close()

	if _, _, err := run(t, []string{"-A", "mybot/1.0", srv.URL}, "", ""); err != nil {
		t.Fatal(err)
	}
	if got.Get("User-Agent") != "mybot/1.0" {
		t.Fatalf("UA override ignored: %q", got.Get("User-Agent"))
	}
}

func TestHeaderOverrideAndRemove(t *testing.T) {
	var got http.Header
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { got = r.Header.Clone() }))
	defer srv.Close()

	if _, _, err := run(t, []string{"-H", "Accept: application/json", "-H", "Sec-Fetch-Mode:", srv.URL}, "", ""); err != nil {
		t.Fatal(err)
	}
	if got.Get("Accept") != "application/json" {
		t.Fatalf("Accept override ignored: %q", got.Get("Accept"))
	}
	if got.Get("Sec-Fetch-Mode") != "" {
		t.Fatalf("Sec-Fetch-Mode should have been removed: %q", got.Get("Sec-Fetch-Mode"))
	}
}

func TestPostFormBody(t *testing.T) {
	var method, body, ctype string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		ctype = r.Header.Get("Content-Type")
		buf := make([]byte, r.ContentLength)
		r.Body.Read(buf)
		body = string(buf)
	}))
	defer srv.Close()

	if _, _, err := run(t, []string{"-d", "a=1&b=2", srv.URL}, "", ""); err != nil {
		t.Fatal(err)
	}
	if method != "POST" || body != "a=1&b=2" || ctype != "application/x-www-form-urlencoded" {
		t.Fatalf("post wrong: %s %q %q", method, body, ctype)
	}
}

func TestGetFoldsDataIntoQuery(t *testing.T) {
	var query string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { query = r.URL.RawQuery }))
	defer srv.Close()

	if _, _, err := run(t, []string{"-G", "-d", "a=1&b=2", srv.URL}, "", ""); err != nil {
		t.Fatal(err)
	}
	if query != "a=1&b=2" {
		t.Fatalf("query = %q", query)
	}
}

func TestIncludeHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Test", "yes")
		w.Write([]byte("body"))
	}))
	defer srv.Close()

	out, _, err := run(t, []string{"-i", srv.URL}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "X-Test: yes") || !strings.HasSuffix(out, "body") {
		t.Fatalf("-i output wrong:\n%s", out)
	}
}

func TestNoFollowByDefault(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			http.Redirect(w, r, "/b", http.StatusFound)
			return
		}
		w.Write([]byte("final"))
	}))
	defer srv.Close()

	out, _, err := run(t, []string{"-w", "%{http_code}", srv.URL + "/a"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasSuffix(out, "302") {
		t.Fatalf("expected 302 without -L, got %q", out)
	}
}

func TestFollowRedirect(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/a" {
			http.Redirect(w, r, "/b", http.StatusFound)
			return
		}
		w.Write([]byte("final"))
	}))
	defer srv.Close()

	out, _, err := run(t, []string{"-L", srv.URL + "/a"}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if out != "final" {
		t.Fatalf("expected followed body, got %q", out)
	}
}

func TestOutputToFile(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.Write([]byte("saved")) }))
	defer srv.Close()

	dir := t.TempDir()
	if _, _, err := run(t, []string{"-o", "resp.txt", srv.URL}, "", dir); err != nil {
		t.Fatal(err)
	}
	data, err := os.ReadFile(filepath.Join(dir, "resp.txt"))
	if err != nil || string(data) != "saved" {
		t.Fatalf("file content = %q err %v", data, err)
	}
}

func TestCookieRoundTrip(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if c, err := r.Cookie("sid"); err == nil {
			w.Write([]byte("sid=" + c.Value))
		}
		http.SetCookie(w, &http.Cookie{Name: "srv", Value: "1"})
	}))
	defer srv.Close()

	dir := t.TempDir()
	out, _, err := run(t, []string{"-b", "sid=abc", "-c", "jar.txt", srv.URL}, "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if out != "sid=abc" {
		t.Fatalf("cookie not sent: %q", out)
	}
	jar, err := os.ReadFile(filepath.Join(dir, "jar.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(jar), "# Netscape HTTP Cookie File") {
		t.Fatalf("jar not written in netscape format:\n%s", jar)
	}
}

func TestBasicAuth(t *testing.T) {
	var user, pass string
	var ok bool
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		user, pass, ok = r.BasicAuth()
	}))
	defer srv.Close()

	if _, _, err := run(t, []string{"-u", "alice:secret", srv.URL}, "", ""); err != nil {
		t.Fatal(err)
	}
	if !ok || user != "alice" || pass != "secret" {
		t.Fatalf("basic auth wrong: %v %q %q", ok, user, pass)
	}
}
