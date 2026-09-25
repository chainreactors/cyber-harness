package aopws

import "testing"

func TestDialURL(t *testing.T) {
	tests := []struct {
		server, path, wantURL, wantToken string
	}{
		{"https://token@example.test/base?region=eu#fragment", "/api/aop/node/ws", "wss://example.test/base/api/aop/node/ws?region=eu", "token"},
		{"http://example.test/", "/api/aop/node/ws", "ws://example.test/api/aop/node/ws", ""},
		{"wss://example.test/prefix/", "runner", "wss://example.test/prefix/runner", ""},
	}
	for _, test := range tests {
		gotURL, gotToken, err := DialURL(test.server, test.path)
		if err != nil || gotURL != test.wantURL || gotToken != test.wantToken {
			t.Fatalf("DialURL(%q, %q) = %q, %q, %v", test.server, test.path, gotURL, gotToken, err)
		}
	}
	if _, _, err := DialURL("ftp://example.test", "/ws"); err == nil {
		t.Fatal("unsupported scheme was accepted")
	}
}
