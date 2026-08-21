package curl

import (
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
