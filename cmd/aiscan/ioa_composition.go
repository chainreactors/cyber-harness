package main

import (
	"context"
	"github.com/chainreactors/cyber/core/resource"
	hostcli "github.com/chainreactors/cyber/pkg/cli"
	cfg "github.com/chainreactors/cyber/pkg/config"
	ioaclient "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	ioaserver "github.com/chainreactors/cyber/pkg/exts/ioa/server"

	recordext "github.com/chainreactors/cyber/pkg/exts/record"
	scannerext "github.com/chainreactors/cyber/pkg/exts/scanner"
	searchext "github.com/chainreactors/cyber/pkg/exts/search"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
	flags "github.com/jessevdk/go-flags"
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
	mustDeclare(ioaclient.Declare(resources, func(registry *hostcli.Registry) error {
		return ioaclient.RegisterCLI(registry, runIOAClientCommand)
	}))
	mustDeclare(ioaserver.Declare(resources, func(registry *hostcli.Registry) error {
		return ioaserver.RegisterCLI(registry, func(ctx context.Context, option ioaserver.Options, env hostcli.Environment) error {
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
		if fields := option.Extensions[ioaclient.ConfigKey]; fields != nil {
			if option.Extensions[ioaserver.ConfigKey] == nil {
				option.Extensions[ioaserver.ConfigKey] = map[string]any{}
			}
			for _, name := range []string{"url", "token"} {
				value, present := fields[name]
				if _, set := option.Extensions[ioaserver.ConfigKey][name]; present && !set {
					option.Extensions[ioaserver.ConfigKey][name] = value
				}
			}
			delete(option.Extensions, ioaclient.ConfigKey)
		}
	}
}

// Legacy node identity is projected once at the configuration boundary.
func applyIdentity(option *cfg.Option) error {
	value, err := ioaclient.ReadOptions(option)
	if err != nil {
		return err
	}
	if option.NodeName == "" {
		option.NodeName = value.NodeName
	}
	return nil
}
