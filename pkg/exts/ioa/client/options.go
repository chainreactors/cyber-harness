package client

import (
	"fmt"
	cfg "github.com/chainreactors/cyber/core/config"
	service "github.com/chainreactors/cyber/tools/ioa"
	"github.com/chainreactors/ioa/protocols"
	"net/url"
	"strings"
)

const ConfigKey = "ioa.client"

// Link-time defaults belong to the extension, not the core option model.
var DefaultURL string
var DefaultSpace = "default"

type Options struct {
	URL      string `long:"ioa-url" config:"url" json:"url" description:"IOA endpoint (defaults to <server-url>/ioa for Web agents)"`
	Token    string `long:"server-token" config:"token" json:"token" description:"Server credential" default-mask:"***"`
	NodeName string `no-flag:"true" config:"node_name" json:"node_name"`
	Space    string `long:"space" config:"space" json:"space" description:"Collaboration space"`
}

func Section() cfg.Section {
	return cfg.Section{Key: ConfigKey, New: func() any { return &Options{URL: DefaultURL, Space: DefaultSpace} }, Secrets: []string{"token"}, Validate: func(v any) error {
		value := v.(*Options)
		if value.URL != "" {
			u, err := url.Parse(value.URL)
			if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") {
				return fmt.Errorf("invalid endpoint URL")
			}
		}
		return nil
	}}
}

func ReadOptions(option *cfg.Option) (Options, error) {
	value := Options{URL: DefaultURL, Space: DefaultSpace}
	if option == nil {
		return value, nil
	}
	var decoded *Options
	var err error
	if option.Resolved != nil {
		decoded, err = cfg.Get[*Options](option.Resolved, ConfigKey)
	} else {
		registry := cfg.NewSections()
		if _, err = registry.Add(Section()); err != nil {
			return Options{}, err
		}
		raw, decodeErr := registry.Decode(ConfigKey, option.Extensions[ConfigKey])
		err = decodeErr
		if err == nil {
			decoded = raw.(*Options)
		}
	}
	if err != nil {
		return Options{}, err
	}
	value = *decoded
	if value.NodeName == "" {
		value.NodeName = option.NodeName
	}
	if _, explicit := option.Extensions[ConfigKey]["url"]; !explicit && value.URL == "" && strings.TrimSpace(option.ServerURL) != "" {
		if u, err := url.Parse(option.ServerURL); err == nil {
			u.Path = strings.TrimRight(u.Path, "/") + "/ioa"
			value.URL = u.String()
		}
	}
	return value, nil
}

type localIdentity struct{ ref protocols.NodeRef }

func (i localIdentity) IOABinding() protocols.IdentityBinding {
	return protocols.IdentityBinding{Namespace: "cyber.memory", Subject: i.ref.URI()}
}
func ConfigFromOption(option *cfg.Option) (*service.Config, error) {
	value, err := ReadOptions(option)
	if err != nil {
		return nil, err
	}
	if value.URL == "" {
		return nil, nil
	}
	return &service.Config{URL: value.URL, NodeID: option.NodeID, NodeName: cfg.ResolveNodeName(value.NodeName), Space: value.Space, RegisterCommands: true, AutoRegister: true, NodeMeta: map[string]any{"client": "cyber"}, Identity: localIdentity{ref: protocols.NodeRef{ID: protocols.NewID(), Authority: "memory://cyber"}}}, nil
}
func Preamble(config service.Config) string {
	if config.Space == "" {
		return ""
	}
	return "IOA collaboration space: " + config.Space
}

func FlagGroup() cfg.FlagGroup { return cfg.FlagGroup{Name: "IOA client", Options: &Options{}} }
