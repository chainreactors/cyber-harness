package config

import (
	"fmt"
	"net/url"
)

// API configures an upstream without changing its request or response schema.
type API struct {
	BaseURL string `json:"base_url"`
	Key     string `json:"key"`
}

// ValidateBaseURL accepts an optional HTTP origin and path prefix. Authentication
// and request parameters belong to the client, rather than the configured URL.
func ValidateBaseURL(value string) error {
	if value == "" {
		return nil
	}
	u, err := url.Parse(value)
	if err != nil || u.Hostname() == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.ForceQuery || u.Fragment != "" {
		return fmt.Errorf("base_url must be an HTTP(S) base URL without credentials, query or fragment")
	}
	return nil
}
