package proxy

import (
	"encoding/base64"
	"testing"
)

func TestParseProxyAuthUser(t *testing.T) {
	basic := func(s string) string { return "Basic " + base64.StdEncoding.EncodeToString([]byte(s)) }
	cases := []struct {
		name   string
		header string
		want   string
	}{
		{"user and password", basic("call-123:secret"), "call-123"},
		{"user only, no colon", basic("call-123"), "call-123"},
		{"empty user with colon", basic(":secret"), ""},
		{"lowercase scheme", "basic " + base64.StdEncoding.EncodeToString([]byte("abc:")), "abc"},
		{"empty header", "", ""},
		{"non-basic scheme", "Bearer token", ""},
		{"malformed base64", "Basic !!!not-base64!!!", ""},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := parseProxyAuthUser(c.header); got != c.want {
				t.Fatalf("parseProxyAuthUser(%q) = %q, want %q", c.header, got, c.want)
			}
		})
	}
}
