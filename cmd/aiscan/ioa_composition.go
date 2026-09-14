package main

import (
	"context"
	cfg "github.com/chainreactors/aiscan/core/config"
	hostcli "github.com/chainreactors/aiscan/pkg/cli"
	client "github.com/chainreactors/aiscan/pkg/exts/ioa/client"
	clientcli "github.com/chainreactors/aiscan/pkg/exts/ioa/client/cli"
	presentation "github.com/chainreactors/aiscan/pkg/exts/ioa/client/console"
	server "github.com/chainreactors/aiscan/pkg/exts/ioa/server"
	servercli "github.com/chainreactors/aiscan/pkg/exts/ioa/server/cli"
	"gopkg.in/yaml.v3"
)

func declareProductCLI(reg *hostcli.Registry) error {
	if err := clientcli.Register(reg, func(ctx context.Context, mode string, option client.Options, output presentation.Options, args presentation.Args, env hostcli.Environment) error {
		return runIOAClientCommand(ctx, mode, option, output, args, env)
	}); err != nil {
		return err
	}
	for _, command := range []string{"agent", "web"} {
		if err := reg.Group("ioa.client", command, client.ConfigKey, client.FlagGroup()); err != nil {
			return err
		}
	}
	return servercli.Register(reg, func(ctx context.Context, option server.Options, env hostcli.Environment) error {
		return runIOAServe(ctx, option, env.Logger)
	})
}
func productSections(serving bool) *cfg.Sections {
	r := cfg.NewSections()
	c, s := client.Section(), server.Section()
	if serving {
		c.Aliases = nil
		s.Aliases = []string{"ioa"}
	}
	if err := r.Register("aiscan", c); err != nil {
		panic(err)
	}
	if err := r.Register("aiscan", s); err != nil {
		panic(err)
	}
	return r
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
