package server

import (
	"context"
	hostcli "github.com/chainreactors/cyber/pkg/cli"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

type CLIExecute func(context.Context, Options, hostcli.Environment) error

func RegisterCLI(reg *hostcli.Registry, run CLIExecute) error {
	for _, command := range []string{"serve", "ioa serve"} {
		command := command
		alias := &struct {
			Addr  string `long:"addr" description:"HTTP listen address"`
			Token string `long:"token" description:"Server access key" default-mask:"***"`
		}{}
		action := hostcli.Action{Persistent: true, Run: func(ctx context.Context, env hostcli.Environment) error {
			section := Section()
			registry := cfg.NewSections()
			_, _ = registry.Add(section)
			v, err := registry.Decode(ConfigKey, env.Config.Extensions[ConfigKey])
			if err != nil {
				return err
			}
			value := *v.(*Options)
			if alias.Addr != "" {
				value.URL = "http://" + alias.Addr
			}
			if alias.Token != "" {
				value.Token = alias.Token
			}
			return run(ctx, value, env)
		}}
		if err := reg.Command(command, "Run the standalone IOA server", alias, action); err != nil {
			return err
		}
		if err := reg.Group(command, ConfigKey, cfg.FlagGroup{Name: "IOA server", Options: &Options{}}); err != nil {
			return err
		}
	}
	return nil
}
