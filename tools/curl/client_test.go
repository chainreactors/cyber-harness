package curl

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
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

func TestDataURLEncode(t *testing.T) {
	var body, ctype string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		b, _ := io.ReadAll(r.Body)
		body, ctype = string(b), r.Header.Get("Content-Type")
	}))
	defer srv.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "q.txt"), []byte("x y\n"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := run(t, []string{
		"--data-urlencode", "q=a b&c=d",
		"--data-urlencode", "file@q.txt",
		"--data-urlencode", "plain",
		srv.URL,
	}, "", dir)
	if err != nil {
		t.Fatal(err)
	}
	want := "q=a%20b%26c%3Dd&file=x%20y%0A&plain"
	if body != want {
		t.Fatalf("body = %q, want %q", body, want)
	}
	if ctype != "application/x-www-form-urlencoded" {
		t.Fatalf("content-type = %q", ctype)
	}
}

func TestMultipartForm(t *testing.T) {
	var field, fileName, fileContent, fileType, ctype string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		ctype = r.Header.Get("Content-Type")
		if err := r.ParseMultipartForm(1 << 20); err != nil {
			t.Errorf("parse multipart: %v", err)
			return
		}
		field = r.FormValue("field")
		f, fh, err := r.FormFile("up")
		if err != nil {
			t.Errorf("form file: %v", err)
			return
		}
		defer f.Close()
		b, _ := io.ReadAll(f)
		fileName, fileContent, fileType = fh.Filename, string(b), fh.Header.Get("Content-Type")
	}))
	defer srv.Close()

	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "a.txt"), []byte("file-bytes"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := run(t, []string{"-F", "field=value", "-F", "up=@a.txt;type=text/plain", srv.URL}, "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.HasPrefix(ctype, "multipart/form-data; boundary=") {
		t.Fatalf("content-type = %q", ctype)
	}
	if field != "value" || fileName != "a.txt" || fileContent != "file-bytes" || fileType != "text/plain" {
		t.Fatalf("multipart wrong: field=%q file=%q %q %q", field, fileName, fileContent, fileType)
	}
}

func TestProxyOverride(t *testing.T) {
	// A stand-in proxy answers directly, so a response proves -x steered the
	// request away from the target.
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.RequestURI, "http://") {
			t.Errorf("proxy got non-absolute URI %q", r.RequestURI)
		}
		w.Write([]byte("via-proxy"))
	}))
	defer proxy.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Write([]byte("direct"))
	}))
	defer target.Close()

	out, _, err := run(t, []string{"-x", proxy.URL, target.URL}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if out != "via-proxy" {
		t.Fatalf("body = %q, want via-proxy", out)
	}
}

func TestDumpHeaders(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Dump-Test", "yes")
		_, _ = w.Write([]byte("body"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	out, _, err := run(t, []string{"-D", "headers.txt", srv.URL}, "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if out != "body" {
		t.Fatalf("body = %q, want body", out)
	}
	headerData, err := os.ReadFile(filepath.Join(dir, "headers.txt"))
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(headerData), "X-Dump-Test: yes") || !strings.Contains(string(headerData), "200 OK") {
		t.Fatalf("dumped headers missing status/header:\n%s", headerData)
	}
}

func TestDumpHeadersIncludesRedirectResponses(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			w.Header().Set("X-First", "yes")
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		w.Header().Set("X-Second", "yes")
		_, _ = w.Write([]byte("final"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	out, _, err := run(t, []string{"-L", "-D", "headers.txt", srv.URL + "/start"}, "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if out != "final" {
		t.Fatalf("body = %q", out)
	}
	data, err := os.ReadFile(filepath.Join(dir, "headers.txt"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(data)
	if !strings.Contains(text, "X-First: yes") || !strings.Contains(text, "X-Second: yes") || !strings.Contains(text, "302 Found") || !strings.Contains(text, "200 OK") {
		t.Fatalf("redirect headers = %q", text)
	}
}

func TestHeadRequest(t *testing.T) {
	var method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		w.Header().Set("X-Head-Test", "yes")
		_, _ = w.Write([]byte("must-not-be-returned"))
	}))
	defer srv.Close()

	out, _, err := run(t, []string{"-I", srv.URL}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodHead {
		t.Fatalf("method = %q, want HEAD", method)
	}
	if !strings.Contains(out, "X-Head-Test: yes") || strings.Contains(out, "must-not-be-returned") {
		t.Fatalf("head output = %q", out)
	}
}

func TestHeadFlagKeepsExplicitMethodButSuppressesBody(t *testing.T) {
	var method string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method = r.Method
		w.Header().Set("X-Head-Test", "yes")
		_, _ = w.Write([]byte("must-not-be-returned"))
	}))
	defer srv.Close()

	out, _, err := run(t, []string{"-I", "-X", "GET", srv.URL}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodGet {
		t.Fatalf("method = %q, want GET", method)
	}
	if !strings.Contains(out, "X-Head-Test: yes") || strings.Contains(out, "must-not-be-returned") {
		t.Fatalf("header-only explicit-method output = %q", out)
	}
}

func TestDefaultPathNormalizationAndPathAsIs(t *testing.T) {
	paths := make(chan string, 4)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.EscapedPath()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	if _, _, err := run(t, []string{srv.URL + "/a/../b/./c"}, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, []string{"--path-as-is", srv.URL + "/a/../b/./c"}, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := <-paths; got != "/b/c" {
		t.Fatalf("default path = %q, want /b", got)
	}
	if got := <-paths; got != "/a/../b/./c" {
		t.Fatalf("--path-as-is path = %q, want /a/../b", got)
	}
}

func TestPathNormalizationPreservesEncodedDots(t *testing.T) {
	paths := make(chan string, 1)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.EscapedPath()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	if _, _, err := run(t, []string{srv.URL + "/%2e%2e/x"}, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := <-paths; got != "/%2e%2e/x" {
		t.Fatalf("encoded dot path = %q", got)
	}
}

func TestFailSuppressesHTTPErrorBody(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not-found-body"))
	}))
	defer srv.Close()

	out, _, err := run(t, []string{"-f", srv.URL}, "", "")
	if err == nil || !strings.Contains(err.Error(), "(22)") {
		t.Fatalf("error = %v, want curl status 22", err)
	}
	if out != "" {
		t.Fatalf("--fail emitted response body %q", out)
	}
}

func TestShortMaxTimeAliasBoundsTransfer(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(150 * time.Millisecond)
		_, _ = w.Write([]byte("late"))
	}))
	defer srv.Close()

	started := time.Now()
	_, _, err := run(t, []string{"-m", "0.01", srv.URL}, "", "")
	if err == nil {
		t.Fatal("-m did not enforce a transfer deadline")
	}
	if elapsed := time.Since(started); elapsed > time.Second {
		t.Fatalf("-m timeout took %s", elapsed)
	}
}

func TestResolveMapsDialWithoutChangingHost(t *testing.T) {
	var gotHost string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		_, _ = w.Write([]byte("resolved"))
	}))
	defer srv.Close()

	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	port := u.Port()
	out, _, err := run(t, []string{
		"--resolve", "example.test:" + port + ":127.0.0.1",
		"http://example.test:" + port + "/path",
	}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if out != "resolved" {
		t.Fatalf("body = %q", out)
	}
	if gotHost != "example.test:"+port {
		t.Fatalf("Host = %q, want example.test:%s", gotHost, port)
	}
}

func TestResolvePreservesTLSServerNameAndHost(t *testing.T) {
	var gotHost string
	srv := httptest.NewTLSServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotHost = r.Host
		_, _ = w.Write([]byte("secure-resolved"))
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, []string{
		"-k", "--resolve", "example.test:" + u.Port() + ":127.0.0.1",
		"https://example.test:" + u.Port() + "/",
	}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if out != "secure-resolved" {
		t.Fatalf("body = %q", out)
	}
	if gotHost != "example.test:"+u.Port() {
		t.Fatalf("Host = %q, want example.test:%s", gotHost, u.Port())
	}
}

func TestResolveRejectsActiveProxy(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("proxy"))
	}))
	defer srv.Close()
	if _, _, err := run(t, []string{
		"--resolve", "example.test:80:127.0.0.1", "-x", srv.URL,
		"http://example.test/",
	}, "", ""); err == nil || !strings.Contains(err.Error(), "--resolve cannot be used with a proxy") {
		t.Fatalf("resolve with proxy error = %v", err)
	}
}

func TestResolveWildcardMapsDial(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("wildcard"))
	}))
	defer srv.Close()
	u, err := url.Parse(srv.URL)
	if err != nil {
		t.Fatal(err)
	}
	out, _, err := run(t, []string{
		"--resolve", "*:" + u.Port() + ":127.0.0.1",
		"http://any-host.invalid:" + u.Port() + "/",
	}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if out != "wildcard" {
		t.Fatalf("wildcard resolve body = %q", out)
	}
}

func TestHTTP11DisablesHTTP2Negotiation(t *testing.T) {
	var proto string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto = r.Proto
		_, _ = w.Write([]byte("ok"))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	if _, _, err := run(t, []string{"--http1.1", "-k", srv.URL}, "", ""); err != nil {
		t.Fatal(err)
	}
	if proto != "HTTP/1.1" {
		t.Fatalf("protocol = %q, want HTTP/1.1", proto)
	}
}

func TestHTTP2Negotiation(t *testing.T) {
	var proto string
	srv := httptest.NewUnstartedServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		proto = r.Proto
		_, _ = w.Write([]byte("ok"))
	}))
	srv.EnableHTTP2 = true
	srv.StartTLS()
	defer srv.Close()

	if _, _, err := run(t, []string{"--http2", "-k", srv.URL}, "", ""); err != nil {
		t.Fatal(err)
	}
	if proto != "HTTP/2.0" {
		t.Fatalf("protocol = %q, want HTTP/2.0", proto)
	}
}
