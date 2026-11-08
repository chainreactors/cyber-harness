package server

import (
	"fmt"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"net/url"
)

const ConfigKey = "ioa.server"

// DefaultURL is the link-time listen URL. Startup overrides use SetDefaultURL.
var DefaultURL string

// SetDefaultURL replaces the link-time listen URL and returns a restore
// function. Call only during startup, before configuration is resolved.
func SetDefaultURL(url string) func() {
	old := DefaultURL
	DefaultURL = url
	return func() { DefaultURL = old }
}

type Options struct {
	URL   string `long:"ioa-url" config:"url" json:"url" description:"IOA listen URL"`
	Token string `long:"server-token" config:"token" json:"token" description:"Server access key" default-mask:"***"`
}

func Section() cfg.Section {
	return cfg.Section{Key: ConfigKey, New: func() any { return &Options{URL: DefaultURL} }, Secrets: []string{"token"}, Validate: func(v any) error {
		value := v.(*Options)
		if value.URL != "" {
			u, err := url.Parse(value.URL)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return fmt.Errorf("invalid listen URL")
			}
		}
		return nil
	}}
}
