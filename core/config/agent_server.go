package config

import (
	"fmt"
	"net/url"
	"strings"
)

// ResolveAgentServerURLs validates the Web/AOP endpoint. IOA remains
// independently configurable and falls back to the Web server's same-origin
// /ioa endpoint when omitted.
func ResolveAgentServerURLs(option *Option) error {
	if option == nil {
		return fmt.Errorf("agent options are required")
	}
	serverURL := strings.TrimSpace(option.ServerURL)
	if serverURL == "" {
		return fmt.Errorf("--server-url is required for web transport")
	}
	serverURL, err := validateAgentServerURL(serverURL)
	if err != nil {
		return err
	}
	option.ServerURL = serverURL
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
