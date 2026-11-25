package config

import (
	"fmt"
	"net/url"
	"strings"

	cfg "github.com/chainreactors/cyber/pkg/config"
)

type Fofa struct {
	cfg.API
	Proxy *string `json:"proxy"`
	Limit *int    `json:"limit"`
}

func FofaSection() cfg.Section {
	return cfg.Section{
		Key: "fofa", New: func() any { return &Fofa{} }, Secrets: []string{"key", "proxy"},
		Normalize: func(value any) error {
			config := value.(*Fofa)
			config.Key = strings.TrimSpace(config.Key)
			if config.BaseURL == "" {
				config.BaseURL = "https://fofa.info"
			}
			return nil
		},
		Validate: func(value any) error {
			config := value.(*Fofa)
			if strings.TrimSpace(config.Key) == "" {
				return fmt.Errorf("key is required")
			}
			if err := cfg.ValidateBaseURL(config.BaseURL); err != nil {
				return err
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
