package curl

import (
	"net/url"
	"testing"
	"time"
)

func TestParseBasicGet(t *testing.T) {
	req, err := Parse([]string{"https://example.com/x"})
	if err != nil {
		t.Fatal(err)
	}
	if req.URL != "https://example.com/x" || req.Method != "GET" {
		t.Fatalf("got %q %q", req.Method, req.URL)
	}
}

func TestParseDataImpliesPost(t *testing.T) {
	req, err := Parse([]string{"-d", "a=1", "https://x"})
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != "POST" {
		t.Fatalf("expected POST, got %q", req.Method)
	}
	if len(req.Data) != 1 || req.Data[0].Value != "a=1" {
		t.Fatalf("bad data: %+v", req.Data)
	}
}

func TestParseExplicitMethodWins(t *testing.T) {
	req, err := Parse([]string{"-XPUT", "-d", "a=1", "https://x"})
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != "PUT" {
		t.Fatalf("expected PUT, got %q", req.Method)
	}
}

func TestParseRepeatedHeaders(t *testing.T) {
	req, err := Parse([]string{"-H", "A: 1", "-H", "B: 2", "-H", "C;", "-H", "D:", "https://x"})
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Headers) != 4 {
		t.Fatalf("want 4 headers, got %d", len(req.Headers))
	}
	if req.Headers[2].Name != "C" || req.Headers[2].Value != "" || req.Headers[2].Remove {
		t.Fatalf("send-empty form wrong: %+v", req.Headers[2])
	}
	if !req.Headers[3].Remove {
		t.Fatalf("remove form wrong: %+v", req.Headers[3])
	}
}

func TestParseShortBundle(t *testing.T) {
	req, err := Parse([]string{"-sSL", "https://x"})
	if err != nil {
		t.Fatal(err)
	}
	if !req.Silent || !req.ShowError || !req.Follow {
		t.Fatalf("bundle not applied: %+v", req)
	}
}

func TestParseBundleTrailingValue(t *testing.T) {
	req, err := Parse([]string{"-so", "out.txt", "https://x"})
	if err != nil {
		t.Fatal(err)
	}
	if !req.Silent || req.Output != "out.txt" {
		t.Fatalf("bundle trailing value wrong: %+v", req)
	}
}

func TestParseDataFileAndBinary(t *testing.T) {
	req, err := Parse([]string{"--data-binary", "@body.bin", "--data-raw", "@literal", "https://x"})
	if err != nil {
		t.Fatal(err)
	}
	if !req.Data[0].File || !req.Data[0].Binary {
		t.Fatalf("data-binary @file wrong: %+v", req.Data[0])
	}
	if req.Data[1].File || !req.Data[1].Raw || req.Data[1].Value != "@literal" {
		t.Fatalf("data-raw should keep @ literal: %+v", req.Data[1])
	}
}

func TestParseTimeouts(t *testing.T) {
	req, err := Parse([]string{"--connect-timeout", "2.5", "--max-time=10", "https://x"})
	if err != nil {
		t.Fatal(err)
	}
	if req.ConnectTimeout != 2500*time.Millisecond || req.MaxTime != 10*time.Second {
		t.Fatalf("timeouts wrong: %v %v", req.ConnectTimeout, req.MaxTime)
	}
}

func TestParseTimeoutOverflowIsRejected(t *testing.T) {
	for _, value := range []string{"9223372036.854775808", "1e100", "NaN", "+Inf"} {
		if _, err := Parse([]string{"-m", value, "https://x"}); err == nil {
			t.Fatalf("Parse accepted overflowing timeout %q", value)
		}
	}
}

func TestParseUnsupportedFlagErrors(t *testing.T) {
	if _, err := Parse([]string{"--http2-prior-knowledge", "https://x"}); err == nil {
		t.Fatal("expected error for unsupported long flag")
	}
	if _, err := Parse([]string{"-Z", "https://x"}); err == nil {
		t.Fatal("expected error for unsupported short flag")
	}
}

func TestParseNoURL(t *testing.T) {
	if _, err := Parse([]string{"-s"}); err == nil {
		t.Fatal("expected error when no URL is given")
	}
}

func TestParseURLFlag(t *testing.T) {
	req, err := Parse([]string{"--url", "https://x/y", "-G", "-d", "a=1"})
	if err != nil {
		t.Fatal(err)
	}
	if req.URL != "https://x/y" || !req.Get || req.Method != "GET" {
		t.Fatalf("url flag / -G wrong: %+v", req)
	}
}

func TestParseMaxRedirsDefault(t *testing.T) {
	req, _ := Parse([]string{"-L", "https://x"})
	if req.MaxRedirs != 50 {
		t.Fatalf("default max-redirs should be 50, got %d", req.MaxRedirs)
	}
}

func TestParseForm(t *testing.T) {
	req, err := Parse([]string{"-F", "field=value", "-F", "up=@a.txt;type=text/plain", "-F", "note=<b.txt", "https://x"})
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != "POST" {
		t.Fatalf("-F should imply POST, got %q", req.Method)
	}
	if len(req.Form) != 3 {
		t.Fatalf("want 3 form parts, got %d", len(req.Form))
	}
	if req.Form[0].Name != "field" || req.Form[0].Value != "value" || req.Form[0].File {
		t.Fatalf("plain field wrong: %+v", req.Form[0])
	}
	if !req.Form[1].File || req.Form[1].Value != "a.txt" || req.Form[1].Type != "text/plain" {
		t.Fatalf("file part wrong: %+v", req.Form[1])
	}
	if !req.Form[2].Content || req.Form[2].Value != "b.txt" {
		t.Fatalf("content part wrong: %+v", req.Form[2])
	}
}

func TestParseFormRequiresName(t *testing.T) {
	if _, err := Parse([]string{"-F", "noname", "https://x"}); err == nil {
		t.Fatal("expected error for -F without name=")
	}
}

func TestParseFormMixingRejected(t *testing.T) {
	if _, err := Parse([]string{"-d", "a=1", "-F", "b=2", "https://x"}); err == nil {
		t.Fatal("expected error combining -d and -F")
	}
	if _, err := Parse([]string{"-G", "-F", "b=2", "https://x"}); err == nil {
		t.Fatal("expected error combining -G and -F")
	}
}

func TestParseDataURLEncode(t *testing.T) {
	req, err := Parse([]string{"--data-urlencode", "q=a b", "https://x"})
	if err != nil {
		t.Fatal(err)
	}
	if req.Method != "POST" || len(req.Data) != 1 || !req.Data[0].URLEncode {
		t.Fatalf("data-urlencode wrong: %+v", req)
	}
}

func TestParseProxy(t *testing.T) {
	req, err := Parse([]string{"-x", "http://127.0.0.1:9000", "https://x"})
	if err != nil {
		t.Fatal(err)
	}
	if req.Proxy != "http://127.0.0.1:9000" {
		t.Fatalf("proxy = %q", req.Proxy)
	}
	req, err = Parse([]string{"--proxy=127.0.0.1:9000", "https://x"})
	if err != nil {
		t.Fatal(err)
	}
	if req.Proxy != "127.0.0.1:9000" {
		t.Fatalf("proxy = %q", req.Proxy)
	}
}

func TestParseCompatibilityFlags(t *testing.T) {
	req, err := Parse([]string{
		"-m", "2.5", "-D", "headers.txt", "-I", "-f", "-N",
		"--http2", "--resolve", "example.test:8443:127.0.0.1",
		"--path-as-is", "https://example.test:8443/a/../b",
	})
	if err != nil {
		t.Fatal(err)
	}
	if req.MaxTime != 2500*time.Millisecond || req.DumpHeader != "headers.txt" {
		t.Fatalf("short compatibility flags parsed incorrectly: %+v", req)
	}
	if !req.Head || req.Method != "HEAD" || !req.Include || !req.Fail || !req.NoBuffer {
		t.Fatalf("head/fail/no-buffer parsed incorrectly: %+v", req)
	}
	if !req.HTTP2 || req.HTTP11 || !req.PathAsIs || len(req.Resolve) != 1 {
		t.Fatalf("transport flags parsed incorrectly: %+v", req)
	}
	entry := req.Resolve[0]
	if entry.Host != "example.test" || entry.Port != "8443" || len(entry.Addresses) != 1 || entry.Addresses[0] != "127.0.0.1" {
		t.Fatalf("resolve parsed incorrectly: %+v", entry)
	}
}

func TestParseHTTPVersionLastOptionWins(t *testing.T) {
	first, err := Parse([]string{"--http2", "--http1.1", "https://x"})
	if err != nil {
		t.Fatal(err)
	}
	if first.HTTP2 || !first.HTTP11 {
		t.Fatalf("last --http1.1 should win: %+v", first)
	}
	second, err := Parse([]string{"--http1.1", "--http2", "https://x"})
	if err != nil {
		t.Fatal(err)
	}
	if !second.HTTP2 || second.HTTP11 {
		t.Fatalf("last --http2 should win: %+v", second)
	}
}

func TestParseResolveIPv6AndTemporary(t *testing.T) {
	req, err := Parse([]string{
		"--resolve", "example.test:443:127.0.0.1",
		"--resolve", "+example.test:443:[::1],127.0.0.2",
		"--resolve", "*:80:127.0.0.3",
		"https://example.test",
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(req.Resolve) != 3 || req.Resolve[1].Host != "example.test" || !req.Resolve[1].Temporary || req.Resolve[1].Addresses[0] != "::1" {
		t.Fatalf("resolve entries = %+v", req.Resolve)
	}
}

func TestParseHeadDoesNotOverrideExplicitMethod(t *testing.T) {
	for _, args := range [][]string{
		{"-X", "GET", "-I", "https://x"},
		{"-I", "-X", "POST", "https://x"},
	} {
		req, err := Parse(args)
		if err != nil {
			t.Fatalf("Parse(%v): %v", args, err)
		}
		if req.Method == "HEAD" || !req.MethodExplicit || !req.Head || !req.Include {
			t.Fatalf("-I should preserve explicit method while enabling header-only mode: %+v", req)
		}
	}
}

func TestParseHeadRejectsRequestBody(t *testing.T) {
	for _, args := range [][]string{
		{"-I", "-d", "a=1", "https://x"},
		{"-I", "-X", "POST", "-F", "a=b", "https://x"},
	} {
		if _, err := Parse(args); err == nil {
			t.Fatalf("Parse(%v) accepted a body with --head", args)
		}
	}
}

func TestNormalizeCurlURLPathRFCExamples(t *testing.T) {
	cases := map[string]string{
		"/a/b/c/./../../g":   "/a/g",
		"/a/b/c/./../../g/":  "/a/g/",
		"/a/b/c/../..":       "/a/",
		"/a/b/c/../../..":    "/",
		"/a/b/c/../../../g":  "/g",
		"/a/b/c/./../../g/.": "/a/g/",
	}
	for input, want := range cases {
		u, err := url.Parse("http://example.test" + input)
		if err != nil {
			t.Fatal(err)
		}
		normalizeCurlURLPath(u)
		if got := u.EscapedPath(); got != want {
			t.Errorf("%s => %s, want %s", input, got, want)
		}
	}
}

func TestNormalizeCurlURLPathRepeatedSlashExamples(t *testing.T) {
	cases := map[string]string{
		"//a///b":    "/a///b",
		"/a//../b":   "/a/b",
		"/a/./b":     "/a/b",
		"/../x":      "/x",
		"/../../x":   "/x",
		"/a//b/../c": "/a//c",
	}
	for input, want := range cases {
		u, _ := url.Parse("http://example.test" + input)
		normalizeCurlURLPath(u)
		if got := u.EscapedPath(); got != want {
			t.Errorf("%s => %s, want %s", input, got, want)
		}
	}
}

func TestParseVersionDoesNotNeedURL(t *testing.T) {
	for _, args := range [][]string{{"--version"}, {"-V", "https://ignored.example"}} {
		req, err := Parse(args)
		if err != nil {
			t.Fatalf("Parse(%v): %v", args, err)
		}
		if !req.Version {
			t.Fatalf("Parse(%v) did not set Version: %+v", args, req)
		}
	}
}
