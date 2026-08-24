package curl

import (
	"bytes"
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

	"github.com/chainreactors/aiscan/pkg/commands"
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
	c := New()
	var egress commands.Egress
	if env != "" {
		egress = commands.ResolveEgress([]string{env}, c.Proxy)
	}
	err = c.do(context.Background(), req, egress, workDir, &out, &errb)
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

func TestRunnerEgressProxy(t *testing.T) {
	proxy := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.HasPrefix(r.RequestURI, "http://") {
			t.Errorf("proxy got non-absolute URI %q", r.RequestURI)
		}
		_, _ = w.Write([]byte("via-runner-egress"))
	}))
	defer proxy.Close()
	target := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("direct"))
	}))
	defer target.Close()

	out, _, err := run(t, []string{target.URL}, "ALL_PROXY="+proxy.URL, "")
	if err != nil {
		t.Fatal(err)
	}
	if out != "via-runner-egress" {
		t.Fatalf("body = %q, want via-runner-egress", out)
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

func TestTraceASCIIFileContainsWireMarkersAndSanitizedData(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("X-Trace", "yes")
		_, _ = w.Write([]byte("hello\x00world\xff"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	out, _, err := run(t, []string{"--trace-ascii", "trace.log", "-d", "secret=body", srv.URL}, "", dir)
	if err != nil {
		t.Fatal(err)
	}
	if out != "hello\x00world\xff" {
		t.Fatalf("response body = %q", out)
	}
	trace, err := os.ReadFile(filepath.Join(dir, "trace.log"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(trace)
	for _, marker := range []string{"=> Send header", "=> Send data", "<= Recv header", "<= Recv data", "0000:"} {
		if !strings.Contains(text, marker) {
			t.Fatalf("trace missing %q:\n%s", marker, text)
		}
	}
	if !strings.Contains(text, "secret=body") || !strings.Contains(text, "hello.world.") {
		t.Fatalf("trace did not contain expected ASCII/body representation:\n%s", text)
	}
}

func TestTraceASCIIDashAndPercentDestinations(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = w.Write([]byte("body"))
	}))
	defer srv.Close()

	out, errOut, err := run(t, []string{"--trace-ascii", "-", srv.URL}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(out, "=> Send header") || !strings.Contains(out, "body") {
		t.Fatalf("trace '-' should share stdout with body: %q", out)
	}
	if errOut != "" {
		t.Fatalf("trace '-' wrote stderr: %q", errOut)
	}

	out, errOut, err = run(t, []string{"--trace-ascii", "%", srv.URL}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if out != "body" || !strings.Contains(errOut, "=> Send header") {
		t.Fatalf("trace '%%' should use stderr: stdout=%q stderr=%q", out, errOut)
	}
}

func TestTraceASCIIFormatterHandlesCRLFAndRawOffsets(t *testing.T) {
	var output bytes.Buffer
	trace := &asciiTrace{w: &output}
	data := append(bytes.Repeat([]byte{'A'}, 64), '\r', '\n', 'B')
	trace.block("<=", "Recv data", data)
	text := output.String()
	if !strings.Contains(text, "0000: "+strings.Repeat("A", 64)+"\n") {
		t.Fatalf("first trace row = %q", text)
	}
	if strings.Contains(text, "0040:") {
		t.Fatalf("trace emitted an empty CRLF row = %q", text)
	}
	if !strings.Contains(text, "0042: B\n") {
		t.Fatalf("CRLF did not advance the raw offset: %q", text)
	}
}

func TestTraceASCIIResponseHeadersUseCurlCallbackBlocks(t *testing.T) {
	resp := &http.Response{
		Proto:  "HTTP/1.1",
		Status: "200 OK",
		Header: http.Header{"X-First": {"one"}, "X-Second": {"two"}},
	}
	blocks := traceResponseHeaderBlocks(resp)
	if len(blocks) != 4 { // status, two fields, terminating CRLF
		t.Fatalf("header blocks = %d, want 4: %#v", len(blocks), blocks)
	}
	if string(blocks[0]) != "HTTP/1.1 200 OK\r\n" || string(blocks[len(blocks)-1]) != "\r\n" {
		t.Fatalf("header block boundaries are not curl-shaped: %#v", blocks)
	}
}

func TestTraceASCIIIncludesRedirectTransfers(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/start" {
			http.Redirect(w, r, "/final", http.StatusFound)
			return
		}
		_, _ = w.Write([]byte("final"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	if _, _, err := run(t, []string{"-L", "--trace-ascii", "trace.log", srv.URL + "/start"}, "", dir); err != nil {
		t.Fatal(err)
	}
	trace, err := os.ReadFile(filepath.Join(dir, "trace.log"))
	if err != nil {
		t.Fatal(err)
	}
	text := string(trace)
	if strings.Count(text, "=> Send header") < 2 || strings.Count(text, "<= Recv header") < 2 {
		t.Fatalf("redirect trace did not include both transfers:\n%s", text)
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

func TestPathNormalizationHandlesEncodedDots(t *testing.T) {
	paths := make(chan string, 2)
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths <- r.URL.EscapedPath()
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()
	if _, _, err := run(t, []string{srv.URL + "/%2e%2e/x"}, "", ""); err != nil {
		t.Fatal(err)
	}
	if _, _, err := run(t, []string{"--path-as-is", srv.URL + "/%2e%2e/x"}, "", ""); err != nil {
		t.Fatal(err)
	}
	if got := <-paths; got != "/x" {
		t.Fatalf("default encoded dot path = %q, want /x", got)
	}
	if got := <-paths; got != "/%2e%2e/x" {
		t.Fatalf("--path-as-is encoded dot path = %q", got)
	}
}

func TestHeadGetDataUsesQuery(t *testing.T) {
	var method, path string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		method, path = r.Method, r.URL.RequestURI()
		w.Header().Set("X-Head-Get", "yes")
		w.WriteHeader(http.StatusNoContent)
	}))
	defer srv.Close()

	out, _, err := run(t, []string{"-I", "-G", "-d", "a=1", srv.URL}, "", "")
	if err != nil {
		t.Fatal(err)
	}
	if method != http.MethodHead || path != "/?a=1" {
		t.Fatalf("request = %s %s, want HEAD /?a=1", method, path)
	}
	if !strings.Contains(out, "X-Head-Get: yes") {
		t.Fatalf("head output = %q", out)
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

func TestFailLeavesOutputFileUntouchedWithoutInclude(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusNotFound)
		_, _ = w.Write([]byte("not-found-body"))
	}))
	defer srv.Close()

	dir := t.TempDir()
	path := filepath.Join(dir, "existing.txt")
	if err := os.WriteFile(path, []byte("old"), 0o644); err != nil {
		t.Fatal(err)
	}
	_, _, err := run(t, []string{"-f", "-o", "existing.txt", srv.URL}, "", dir)
	if err == nil || !strings.Contains(err.Error(), "(22)") {
		t.Fatalf("error = %v, want curl status 22", err)
	}
	data, readErr := os.ReadFile(path)
	if readErr != nil || string(data) != "old" {
		t.Fatalf("--fail changed output file: %q err=%v", data, readErr)
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
	if err == nil || !strings.Contains(err.Error(), "(28)") {
		t.Fatalf("-m timeout error = %v, want curl status 28", err)
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
