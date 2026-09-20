package client

import (
	"strings"
	"testing"
)

func TestRedactIOAURL(t *testing.T) {
	raw := "http://be7c1b68264bae5a37570e2785fea0725dd26760f49037d7081939178e81098f@127.0.0.1:3000/ioa"
	got := redactIOAURL(raw)
	if strings.Contains(got, "be7c1b68") {
		t.Errorf("redactIOAURL leaked token: %q", got)
	}
	if got != "http://127.0.0.1:3000/ioa" {
		t.Errorf("redactIOAURL = %q, want http://127.0.0.1:3000/ioa", got)
	}
	if got := redactIOAURL("http://127.0.0.1:3000/ioa"); got != "http://127.0.0.1:3000/ioa" {
		t.Errorf("redactIOAURL(no token) = %q", got)
	}
}

func TestRedactIOAURLFallbackOnMalformedURL(t *testing.T) {
	raw := "http://super-secret-token@127.0.0.1:3000/ioa/%zz"
	got := redactIOAURL(raw)
	if strings.Contains(got, "super-secret-token") {
		t.Fatalf("redactIOAURL malformed leaked token: %q", got)
	}
	if !strings.Contains(got, "127.0.0.1:3000") {
		t.Fatalf("redactIOAURL malformed dropped host: %q", got)
	}
}

func TestShortID(t *testing.T) {
	if got := shortID("de12abca01d7a92f1630e21f642a37e0"); got != "de12abca" {
		t.Errorf("shortID = %q, want de12abca", got)
	}
	if got := shortID("abc"); got != "abc" {
		t.Errorf("shortID(short) = %q, want abc", got)
	}
}

// TestStatusSampleRender prints a realistic /status box (color off) so the fix
// is visible in `go test -v` output.
