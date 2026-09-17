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
	recordext "github.com/chainreactors/cyber/pkg/exts/record"
	scannerext "github.com/chainreactors/cyber/pkg/exts/scanner"
	searchext "github.com/chainreactors/cyber/pkg/exts/search"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	flags "github.com/jessevdk/go-flags"
	"gopkg.in/yaml.v3"
	"sync"
)

// defaultSections is the declaration graph of a host that contributes no CLI
// parser of its own. It is identical on every call and sealed before it is
// returned -- Add is refused and nothing else writes -- so the nine callers
// that only read it share one instead of rebuilding six Declares apiece.
var defaultSections = sync.OnceValue(func() *cfg.Sections { return declareResources(nil, nil) })

func declareResources(cli *hostcli.Registry, agentOptions *cfg.AgentOptions) *cfg.Sections {
	resources := resource.New()
	sections := cfg.NewSections()
	localCLI := cli == nil
	if cli == nil {
		cli = hostcli.New(flags.NewParser(&cliOptions{}, flags.None))
	}
	if _, err := resource.Define[hostcli.Contribution](resources, cli); err != nil {
		panic(err)
	}
	if _, err := resource.Define[cfg.Section](resources, sections); err != nil {
		panic(err)
	}
	if _, err := resource.Define[cfg.Connection](resources, sections.ConnectionPoint()); err != nil {
		panic(err)
	}
	mustDeclare := func(err error) {
		if err != nil {
			panic(err)
		}
	}
	mustDeclare(client.Declare(resources, func(registry *hostcli.Registry) error {
		return clientcli.Register(registry, runIOAClientCommand)
	}))
	mustDeclare(server.Declare(resources, func(registry *hostcli.Registry) error {
		return servercli.Register(registry, func(ctx context.Context, option server.Options, env hostcli.Environment) error {
			return runIOAServe(ctx, option, env.Logger)
		})
	}))
	mustDeclare(recordext.Declare(resources))
	mustDeclare(scannerext.Declare(resources))
	mustDeclare(searchext.Declare(resources))
	if agentOptions != nil {
		mustDeclare(sessionext.Declare(resources, agentOptions))
	}
	resources.Freeze()
	if localCLI {
		mustDeclare(cli.Seal())
	}
	sections.Seal()
	return sections
}

func finalizeOptions(option *cfg.Option, action *hostcli.Action) {
	serving := action != nil && action.Persistent
	option.Sections = defaultSections()
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

func defaultConfig() string {
	defaults := defaultSections().Defaults()
	// Omission keeps same-origin URL derivation; an explicit empty URL disables it.
	if defaults[client.ConfigKey]["url"] == "" {
		delete(defaults[client.ConfigKey], "url")
	}
	document := map[string]any{"extensions": defaults}
	b, _ := yaml.Marshal(document)
	return cfg.InitDefaultConfig() + "\n" + string(b)
}

// Legacy node identity is projected once at the configuration boundary.
func applyIdentity(option *cfg.Option) error {
	value, err := client.ReadOptions(option)
	if err != nil {
		return err
	}
	if option.NodeName == "" {
		option.NodeName = value.NodeName
	}
	return nil
}
