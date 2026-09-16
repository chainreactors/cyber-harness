package main

import (
	"context"
	cfg "github.com/chainreactors/cyber/core/config"
	hostcli "github.com/chainreactors/cyber/pkg/cli"
	client "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	clientcli "github.com/chainreactors/cyber/pkg/exts/ioa/client/cli"
	server "github.com/chainreactors/cyber/pkg/exts/ioa/server"
	servercli "github.com/chainreactors/cyber/pkg/exts/ioa/server/cli"
	"github.com/chainreactors/cyber/pkg/exts/record"
	settings "github.com/chainreactors/cyber/pkg/exts/settings"
	"gopkg.in/yaml.v3"
)

func declareProductCLI(reg *hostcli.Registry) error {
	e, err := settings.New(productDeclarations(false))
	if err != nil {
		return err
	}
	return e.Declare(reg)
}

// Product declarations are inert and independent of runtime IOA connections.
// Other profiles select only the declaration layers they actually expose.
func productDeclarations(serving bool) []settings.Declaration {
	c, s := client.Section(), server.Section()
	if serving {
		c.Aliases = nil
		s.Aliases = []string{"ioa"}
	}
	return []settings.Declaration{
		{ID: client.ConfigKey, Config: []cfg.Section{c},
			Flags: []settings.Flag{
				{Command: "agent", Key: client.ConfigKey, Group: client.FlagGroup()},
				{Command: "web", Key: client.ConfigKey, Group: client.FlagGroup()},
			},
			DeclareCLI: func(reg *hostcli.Registry) error {
				return clientcli.Register(reg, runIOAClientCommand)
			}},
		{ID: server.ConfigKey, Config: []cfg.Section{s}, DeclareCLI: func(reg *hostcli.Registry) error {
			return servercli.Register(reg, func(ctx context.Context, option server.Options, env hostcli.Environment) error {
				return runIOAServe(ctx, option, env.Logger)
			})
		}},
		{ID: record.ConfigKey, Config: []cfg.Section{record.Section()}},
	}
}

func productSections(serving bool) *cfg.Sections {
	e, err := settings.New(productDeclarations(serving))
	if err != nil {
		panic(err)
	}
	return e.Sections()
}
func finalizeProductOptions(option *cfg.Option, action *hostcli.Action) {
	serving := action != nil && action.Persistent
	option.Sections = productSections(serving)
	if serving {
		if fields := option.Extensions[client.ConfigKey]; fields != nil {
			if option.Extensions[server.ConfigKey] == nil {
				option.Extensions[server.ConfigKey] = map[string]any{}
			}
			for _, name := range []string{"url", "token"} {
				value, present := fields[name]
				if _, set := option.Extensions[server.ConfigKey][name]; present && !set {
					option.Extensions[server.ConfigKey][name] = value
				}
			}
			delete(option.Extensions, client.ConfigKey)
		}
	}
}

func productDefaultConfig() string {
	defaults := productSections(false).Defaults()
	// Omission keeps same-origin URL derivation; an explicit empty URL disables it.
	if defaults[client.ConfigKey]["url"] == "" {
		delete(defaults[client.ConfigKey], "url")
	}
	document := map[string]any{"extensions": defaults}
	b, _ := yaml.Marshal(document)
	return cfg.InitDefaultConfig() + "\n" + string(b)
}

// Legacy node identity is projected once at the product boundary.
func applyProductIdentity(option *cfg.Option) error {
	value, err := client.ReadOptions(option)
	if err != nil {
		return err
	}
	if option.NodeName == "" {
		option.NodeName = value.NodeName
	}
	return nil
}
