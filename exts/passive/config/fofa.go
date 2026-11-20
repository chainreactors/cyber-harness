package config

import (
	"fmt"
	"net/url"
	"strings"

	cfg "github.com/chainreactors/cyber/pkg/config"
)

type Fofa struct {
	Key   string  `json:"key"`
	URL   string  `json:"url"`
	Proxy *string `json:"proxy"`
	Limit *int    `json:"limit"`
}

func FofaSection() cfg.Section {
	return cfg.Section{
		Key: "fofa", New: func() any { return &Fofa{} }, Secrets: []string{"key", "proxy"},
		Validate: func(value any) error {
			config := value.(*Fofa)
			if strings.TrimSpace(config.Key) == "" {
				return fmt.Errorf("key is required")
			}
			if config.URL != "" {
				u, err := url.Parse(config.URL)
				if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
					return fmt.Errorf("url must be an HTTP search endpoint without credentials or query")
				}
			}
			if config.Limit != nil && *config.Limit < 0 {
				return fmt.Errorf("limit must not be negative")
			}
			if config.Proxy != nil && *config.Proxy != "" {
				u, err := url.Parse(*config.Proxy)
				if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https" && u.Scheme != "socks5" && u.Scheme != "socks5h") {
					return fmt.Errorf("proxy must be an HTTP or SOCKS URL")
				}
			}
			return nil
		},
	}
}
