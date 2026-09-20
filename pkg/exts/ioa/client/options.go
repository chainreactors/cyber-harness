package client

import (
	"fmt"
	cfg "github.com/chainreactors/cyber/pkg/config"
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
	return &service.Config{URL: accessKeyURL(value.URL, value.Token), NodeID: option.NodeID, NodeName: cfg.ResolveNodeName(value.NodeName), Space: value.Space, RegisterCommands: true, AutoRegister: true, NodeMeta: map[string]any{"client": "cyber"}, Identity: localIdentity{ref: protocols.NodeRef{ID: protocols.NewID(), Authority: "memory://cyber"}}}, nil
}

// accessKeyURL folds the configured credential into the endpoint as userinfo.
// The credential is the IOA server access key — that is what the `--server-token`
// flag, the Web "Access Token" field and the `ioa serve` mirror all mean — and
// the SDK reads an access key only from URL userinfo. Handing the same string to
// NewClientWithToken instead sends it verbatim as a bearer token, which the
// server rejects: access keys are accepted by POST /auth/register alone, and the
// token that registration issues is what the remaining endpoints want.
func accessKeyURL(endpoint, token string) string {
	if token == "" {
		return endpoint
	}
	parsed, err := url.Parse(endpoint)
	if err != nil || parsed.Host == "" || parsed.User != nil {
		return endpoint
	}
	parsed.User = url.User(token)
	return parsed.String()
}
func FlagGroup() cfg.FlagGroup { return cfg.FlagGroup{Name: "IOA client", Options: &Options{}} }
