package web

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

func ValidateTarget(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return "", fmt.Errorf("target is required")
	}

	if strings.Contains(raw, ",") || strings.Contains(raw, " ") {
		return "", fmt.Errorf("only a single target is allowed")
	}

	if idx := strings.Index(raw, "/"); idx >= 0 {
		prefix := raw[:idx]
		if net.ParseIP(prefix) != nil {
			return "", fmt.Errorf("CIDR ranges are not allowed; provide a single IP or URL")
		}
		if host, _, err := net.SplitHostPort(prefix); err == nil && net.ParseIP(host) != nil {
			return "", fmt.Errorf("CIDR ranges are not allowed; provide a single IP or URL")
		}
	}

	if strings.Contains(raw, "://") {
		parsed, err := url.Parse(raw)
		if err != nil || parsed.Hostname() == "" {
			return "", fmt.Errorf("invalid URL: %s", raw)
		}
		if parsed.Scheme != "http" && parsed.Scheme != "https" {
			return "", fmt.Errorf("only http and https URLs are allowed")
		}
		return raw, nil
	}

	if host, _, err := net.SplitHostPort(raw); err == nil {
		if net.ParseIP(host) != nil {
			return raw, nil
		}
		return raw, nil
	}

	if net.ParseIP(raw) != nil {
		return raw, nil
	}

	if isValidHostname(raw) {
		return raw, nil
	}

	return "", fmt.Errorf("invalid target: %s (expected IP, IP:port, hostname, or URL)", raw)
}

func ValidateMode(mode string) (string, error) {
	mode = strings.TrimSpace(strings.ToLower(mode))
	if mode == "" {
		return "quick", nil
	}
	switch mode {
	case "quick", "full":
		return mode, nil
	default:
		return "", fmt.Errorf("invalid mode %q: must be quick or full", mode)
	}
}

func isValidHostname(s string) bool {
	if len(s) == 0 || len(s) > 253 {
		return false
	}
	if !strings.Contains(s, ".") {
		return false
	}
	for _, c := range s {
		if !((c >= 'a' && c <= 'z') || (c >= 'A' && c <= 'Z') || (c >= '0' && c <= '9') || c == '-' || c == '.') {
			return false
		}
	}
	return true
}
