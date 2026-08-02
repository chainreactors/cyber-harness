package config

import (
	"fmt"
	"net/url"
	"strings"
)

// ResolveAgentServerURLs makes --server-url the canonical Web/AOP endpoint.
// Deprecated --web-url is only an alias. IOA remains independently configurable
// and falls back to the Web server's same-origin /ioa endpoint when omitted.
func ResolveAgentServerURLs(option *Option) error {
	if option == nil {
		return fmt.Errorf("agent options are required")
	}
	serverURL := strings.TrimSpace(option.ServerURL)
	legacyWebURL := strings.TrimSpace(option.WebURL)
	if serverURL == "" && legacyWebURL == "" {
		return fmt.Errorf("--server-url is required for web transport")
	}
	if serverURL != "" && legacyWebURL != "" && normalizeServerURL(serverURL) != normalizeServerURL(legacyWebURL) {
		return fmt.Errorf("--server-url and deprecated --web-url refer to different AIScan servers")
	}
	if serverURL == "" {
		serverURL = legacyWebURL
	}
	serverURL, err := validateAgentServerURL(serverURL)
	if err != nil {
		return err
	}
	option.ServerURL = serverURL
	option.WebURL = serverURL
	if strings.TrimSpace(option.IOAURL) == "" {
		option.IOAURL = deriveIOAURL(serverURL)
	}
	return nil
}

func validateAgentServerURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil || parsed.Scheme == "" || parsed.Host == "" {
		return "", fmt.Errorf("invalid AIScan server URL %q", raw)
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return "", fmt.Errorf("AIScan server URL must use http or https")
	}
	parsed.Fragment = ""
	return strings.TrimRight(parsed.String(), "/"), nil
}

func deriveIOAURL(serverURL string) string {
	parsed, err := url.Parse(serverURL)
	if err != nil {
		return ""
	}
	parsed.Path = strings.TrimRight(parsed.Path, "/") + "/ioa"
	parsed.RawPath = ""
	return strings.TrimRight(parsed.String(), "/")
}

func normalizeServerURL(value string) string {
	return strings.TrimRight(strings.TrimSpace(value), "/")
}
