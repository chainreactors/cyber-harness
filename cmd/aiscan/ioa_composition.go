package main

import (
	"context"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/resource"
	hostcli "github.com/chainreactors/cyber/pkg/cli"
	client "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	clientcli "github.com/chainreactors/cyber/pkg/exts/ioa/client/cli"
	server "github.com/chainreactors/cyber/pkg/exts/ioa/server"
	servercli "github.com/chainreactors/cyber/pkg/exts/ioa/server/cli"
	"github.com/chainreactors/cyber/pkg/exts/record"
	"gopkg.in/yaml.v3"
)

func declareProductCLI(resources *resource.Registry) error {
	if err := clientcli.Declare(resources, runIOAClientCommand); err != nil {
		return err
	}
	if err := servercli.Declare(resources, func(ctx context.Context, option server.Options, env hostcli.Environment) error {
		return runIOAServe(ctx, option, env.Logger)
	}); err != nil {
		return err
	}
	_, err := resource.Add[hostcli.Contribution](resources,
		func(registry *hostcli.Registry) error {
			return registry.Group("agent", client.ConfigKey, client.FlagGroup())
		},
		func(registry *hostcli.Registry) error {
			return registry.Group("web", client.ConfigKey, client.FlagGroup())
		},
	)
	return err
}

func declareProductConfig(resources *resource.Registry, serving bool) error {
	if !serving {
		if err := client.Declare(resources); err != nil {
			return err
		}
		if err := server.Declare(resources); err != nil {
			return err
		}
	} else {
		clientSection, serverSection := client.Section(), server.Section()
		clientSection.Aliases = nil
		serverSection.Aliases = []string{"ioa"}
		if _, err := resource.Add[cfg.Section](resources, clientSection, serverSection); err != nil {
			return err
		}
	}
	return record.Declare(resources)
}

func productSections(serving bool) *cfg.Sections {
	resources := resource.New()
	sections := cfg.NewSections()
	if _, err := resource.Define[cfg.Section](resources, sections); err != nil {
		panic(err)
	}
	if err := declareProductConfig(resources, serving); err != nil {
		panic(err)
	}
	resources.Freeze()
	sections.Seal()
	return sections
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
