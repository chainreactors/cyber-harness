package commands

import "strings"

// Egress is the per-invocation outbound route injected by the Runner.
// ProxyURL carries the call-scoped Hub identity; CAPath is populated only when
// the Hub is actively intercepting HTTPS traffic.
type Egress struct {
	ProxyURL string
	CAPath   string
}

// ResolveExecutionEgress resolves the route for one command invocation. The
// execution environment is intentionally authoritative for call-scoped Runner
// state; fallbackProxy is only the command's startup default.
func ResolveExecutionEgress(execution *Execution, fallbackProxy string) Egress {
	if execution == nil {
		return ResolveEgress(nil, fallbackProxy)
	}
	return ResolveEgress(execution.Env, fallbackProxy)
}

var (
	// proxyEnvNames preserves the conventional environment surface exposed to
	// child processes. Keep both cases because Windows and POSIX callers differ
	// in how they spell environment keys.
	proxyEnvNames = []string{
		"ALL_PROXY", "all_proxy",
		"HTTP_PROXY", "http_proxy",
		"HTTPS_PROXY", "https_proxy",
	}
	proxyLookupOrder = []string{
		"ALL_PROXY", "all_proxy",
		"HTTPS_PROXY", "https_proxy",
		"HTTP_PROXY", "http_proxy",
	}
	caEnvNames = []string{
		"CURL_CA_BUNDLE", "SSL_CERT_FILE", "NODE_EXTRA_CA_CERTS",
		"REQUESTS_CA_BUNDLE", "GIT_SSL_CAINFO",
	}
)

// ResolveEgress resolves one invocation's egress from an environment slice.
// The explicit order is independent of how the caller sorted or assembled its
// environment, and falls back to the command's startup proxy when no call
// scoped value is present.
func ResolveEgress(env []string, fallbackProxy string) Egress {
	values := make(map[string]string, len(env))
	for _, item := range env {
		key, value, ok := strings.Cut(item, "=")
		if !ok {
			continue
		}
		values[key] = value
	}

	resolved := Egress{}
	for _, key := range proxyLookupOrder {
		if value := strings.TrimSpace(values[key]); value != "" {
			resolved.ProxyURL = value
			break
		}
	}
	if resolved.ProxyURL == "" {
		resolved.ProxyURL = strings.TrimSpace(fallbackProxy)
	}
	for _, key := range caEnvNames {
		if value := strings.TrimSpace(values[key]); value != "" {
			resolved.CAPath = value
			break
		}
	}
	return resolved
}

// EgressEnvironment returns the environment entries used by both built-in
// tools and child shell commands. An empty proxy intentionally produces no
// proxy variables, preserving direct-command behavior for non-Runner callers.
func EgressEnvironment(proxyURL, caPath string) []string {
	proxyURL = strings.TrimSpace(proxyURL)
	if proxyURL == "" {
		return nil
	}
	env := make([]string, 0, len(proxyEnvNames)+len(caEnvNames))
	for _, key := range proxyEnvNames {
		env = append(env, key+"="+proxyURL)
	}
	if caPath = strings.TrimSpace(caPath); caPath != "" {
		for _, key := range caEnvNames {
			env = append(env, key+"="+caPath)
		}
	}
	return env
}
