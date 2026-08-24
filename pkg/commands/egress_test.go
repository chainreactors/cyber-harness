package commands

import "testing"

func TestResolveEgressUsesStableProxyPrecedence(t *testing.T) {
	tests := []struct {
		name      string
		env       []string
		fallback  string
		wantProxy string
		wantCA    string
	}{
		{
			name:      "all proxy wins independent of env order",
			env:       []string{"HTTP_PROXY=http://http", "ALL_PROXY=http://all"},
			fallback:  "http://startup",
			wantProxy: "http://all",
		},
		{
			name:      "https beats http",
			env:       []string{"HTTP_PROXY=http://http", "HTTPS_PROXY=http://https"},
			wantProxy: "http://https",
		},
		{
			name:      "fallback",
			env:       []string{"ALL_PROXY=", "HTTPS_PROXY= "},
			fallback:  " http://startup ",
			wantProxy: "http://startup",
		},
		{
			name:   "ca precedence",
			env:    []string{"GIT_SSL_CAINFO=/git.pem", "SSL_CERT_FILE=/ssl.pem", "CURL_CA_BUNDLE=/curl.pem"},
			wantCA: "/curl.pem",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got := ResolveEgress(tt.env, tt.fallback)
			if got.ProxyURL != tt.wantProxy || got.CAPath != tt.wantCA {
				t.Fatalf("ResolveEgress() = %#v, want proxy=%q ca=%q", got, tt.wantProxy, tt.wantCA)
			}
		})
	}
}

func TestResolveExecutionEgressUsesInvocationEnvironment(t *testing.T) {
	execution := &Execution{
		Env: []string{"ALL_PROXY=http://call-scoped", "SSL_CERT_FILE=/call-ca.pem"},
	}
	got := ResolveExecutionEgress(execution, "http://startup")
	if got.ProxyURL != "http://call-scoped" || got.CAPath != "/call-ca.pem" {
		t.Fatalf("ResolveExecutionEgress() = %#v, want call-scoped proxy and CA", got)
	}

	got = ResolveExecutionEgress(&Execution{}, "http://startup")
	if got.ProxyURL != "http://startup" {
		t.Fatalf("ResolveExecutionEgress(empty env) = %#v, want startup fallback", got)
	}
}

func TestEgressEnvironmentUsesSharedSurface(t *testing.T) {
	got := EgressEnvironment("http://hub", "/tmp/mitm-ca.pem")
	want := []string{
		"ALL_PROXY=http://hub", "all_proxy=http://hub",
		"HTTP_PROXY=http://hub", "http_proxy=http://hub",
		"HTTPS_PROXY=http://hub", "https_proxy=http://hub",
		"CURL_CA_BUNDLE=/tmp/mitm-ca.pem", "SSL_CERT_FILE=/tmp/mitm-ca.pem",
		"NODE_EXTRA_CA_CERTS=/tmp/mitm-ca.pem", "REQUESTS_CA_BUNDLE=/tmp/mitm-ca.pem",
		"GIT_SSL_CAINFO=/tmp/mitm-ca.pem",
	}
	if len(got) != len(want) {
		t.Fatalf("EgressEnvironment() = %#v, want %#v", got, want)
	}
	for i := range want {
		if got[i] != want[i] {
			t.Fatalf("EgressEnvironment()[%d] = %q, want %q", i, got[i], want[i])
		}
	}
	if got := EgressEnvironment("", "/tmp/mitm-ca.pem"); got != nil {
		t.Fatalf("empty proxy environment = %#v, want nil", got)
	}
}
