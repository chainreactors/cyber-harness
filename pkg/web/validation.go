package web

import (
	"fmt"
	"net"
	"net/url"
	"strings"

	agentprovider "github.com/chainreactors/aiscan/agent/provider"
	config "github.com/chainreactors/aiscan/core/config"
	configpb "github.com/chainreactors/aiscan/pkg/types/config"
)

// ValidateLLMConfig accepts zero limits as "use the model default" and rejects
// incomplete profiles before an invalid configuration can be persisted.
func ValidateLLMConfig(cfg *configpb.LLMConfig) error {
	if cfg == nil {
		return nil
	}
	for i, profile := range cfg.Providers {
		profile = config.NormalizeLLMProvider(profile)
		if !agentprovider.IsSupportedProvider(profile.Provider) {
			return fmt.Errorf("LLM provider %q is unsupported: use openai or anthropic", profile.Provider)
		}
		if strings.TrimSpace(profile.Model) == "" {
			name := strings.TrimSpace(profile.Name)
			if name == "" {
				name = strings.TrimSpace(profile.Id)
			}
			if name == "" {
				name = fmt.Sprintf("#%d", i+1)
			}
			return fmt.Errorf("LLM profile %q model is required", name)
		}
		if profile.MaxTokens < 0 {
			return fmt.Errorf("LLM max_tokens must be zero or positive")
		}
		if profile.ContextWindow < 0 {
			return fmt.Errorf("LLM context_window must be zero or positive")
		}
	}
	return nil
}

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
