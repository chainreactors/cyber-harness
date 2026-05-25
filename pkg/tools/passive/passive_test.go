//go:build full

package passive

import (
	"encoding/json"
	"testing"

	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	"github.com/projectdiscovery/uncover/sources"
)

func TestUncoverPythonFofaShape(t *testing.T) {
	raw, _ := json.Marshal(engine.RawFofa{
		IP: "1.2.3.4", Port: "443", Host: "https://example.com",
		Domain: "example.com", Title: "Example", ICP: "ICP1",
	})
	b, err := json.Marshal(uncoverPython("fofa", []sources.Result{{
		Source: "fofa", IP: "1.2.3.4", Port: 443, Host: "example.com", Raw: raw,
	}}))
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	for _, key := range []string{"ip", "port", "url", "domain", "title", "icp"} {
		if _, ok := got[0][key]; !ok {
			t.Fatalf("missing key %q in %v", key, got[0])
		}
	}
	if _, ok := got[0]["source"]; ok {
		t.Fatalf("source should not be in fofa Python shape: %v", got[0])
	}
}

func TestUncoverPythonHunterShape(t *testing.T) {
	raw, _ := json.Marshal(engine.RawHunter{
		IP: "1.2.3.4", Port: "443", URL: "http://example.com:443",
		Domain: "example.com", Status: "200", Company: "Example Inc",
		Frame: "nginx,spring", Title: "Example", ICP: "ICP1",
	})
	b, err := json.Marshal(uncoverPython("hunter", []sources.Result{{
		Source: "hunter", IP: "1.2.3.4", Port: 443, Host: "example.com", Raw: raw,
	}}))
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	for _, key := range []string{"ip", "port", "url", "domain", "status", "company", "frame", "title", "icp"} {
		if _, ok := got[0][key]; !ok {
			t.Fatalf("missing key %q in %v", key, got[0])
		}
	}
	if got[0]["frame"] != "nginx,spring" {
		t.Fatalf("frame = %v", got[0]["frame"])
	}
}

func TestUncoverPythonGenericShape(t *testing.T) {
	b, err := json.Marshal(uncoverPython("shodan", []sources.Result{{
		Source: "shodan", IP: "5.6.7.8", Port: 80, Host: "example.org",
		Url: "http://example.org:80",
	}}))
	if err != nil {
		t.Fatal(err)
	}
	var got []map[string]any
	if err := json.Unmarshal(b, &got); err != nil {
		t.Fatal(err)
	}
	if len(got) != 1 {
		t.Fatalf("len = %d, want 1", len(got))
	}
	for _, key := range []string{"ip", "port", "url", "host", "source"} {
		if _, ok := got[0][key]; !ok {
			t.Fatalf("missing key %q in %v", key, got[0])
		}
	}
	if got[0]["source"] != "shodan" {
		t.Fatalf("source = %v, want shodan", got[0]["source"])
	}
}

func TestSplitSource(t *testing.T) {
	src, rest, help, err := splitSource([]string{"-s", "fofa", "domain=\"x.com\""})
	if err != nil || help || src != "fofa" || len(rest) != 1 || rest[0] != `domain="x.com"` {
		t.Fatalf("src=%q rest=%v help=%v err=%v", src, rest, help, err)
	}

	_, _, help, _ = splitSource([]string{"-h"})
	if !help {
		t.Fatal("expected help")
	}

	_, _, _, err = splitSource([]string{"-n", "foo"})
	if err == nil {
		t.Fatal("expected error when -s missing")
	}
}

func TestParseQueryArgs(t *testing.T) {
	q, err := parseQueryArgs([]string{`domain="example.com"`})
	if err != nil || q != `domain="example.com"` {
		t.Fatalf("q=%q err=%v", q, err)
	}

	_, err = parseQueryArgs([]string{})
	if err == nil {
		t.Fatal("expected error for missing query")
	}

	_, err = parseQueryArgs([]string{"a", "b"})
	if err == nil {
		t.Fatal("expected error for multiple positional args")
	}
}
