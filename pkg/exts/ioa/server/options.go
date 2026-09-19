package server

import (
	"fmt"
	cfg "github.com/chainreactors/cyber/core/config"
	"net/url"
)

const ConfigKey = "ioa.server"

var DefaultURL string

type Options struct {
	URL   string `long:"ioa-url" config:"url" json:"url" description:"IOA listen URL"`
	Token string `long:"server-token" config:"token" json:"token" description:"Server access key" default-mask:"***"`
}

func Section() cfg.Section {
	return cfg.Section{Key: ConfigKey, New: func() any { return &Options{URL: DefaultURL} }, Secrets: []string{"token"}, AliasFields: func(fields map[string]any) (map[string]any, error) {
		delete(fields, "space")
		delete(fields, "node_name")
		return fields, nil
	}, Validate: func(v any) error {
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
