// Package relay provides Cyber Relay as one capability of its own: the relay
// command and the cyber-relay skill, bound from gateway.relay. Without an
// enabled gateway.relay nothing is loaded, so the agent never sees the relay.
package relay

import (
	"fmt"
	"strings"

	cfg "github.com/chainreactors/cyber/pkg/config"
)

// Kind is the gateway service kind, and the default instance name.
const Kind = "relay"

func Section() cfg.Section {
	return cfg.Section{
		Key: Kind, New: func() any { return &cfg.API{} }, Secrets: []string{"key"},
		Normalize: func(value any) error {
			api := value.(*cfg.API)
			api.BaseURL, api.Key = strings.TrimSpace(api.BaseURL), strings.TrimSpace(api.Key)
			return nil
		},
		Validate: func(value any) error {
			api := value.(*cfg.API)
			if api.BaseURL == "" {
				return fmt.Errorf("base_url is required")
			}
			if api.Key == "" {
				return fmt.Errorf("key is required")
			}
			return cfg.ValidateBaseURL(api.BaseURL)
		},
	}
}
