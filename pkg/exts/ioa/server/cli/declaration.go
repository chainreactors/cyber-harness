package cli

import (
	"context"
	cfg "github.com/chainreactors/aiscan/core/config"
	hostcli "github.com/chainreactors/aiscan/pkg/cli"
	server "github.com/chainreactors/aiscan/pkg/exts/ioa/server"
)

type Execute func(context.Context, server.Options, hostcli.Environment) error

func Register(reg *hostcli.Registry, run Execute) error {
	for _, command := range []string{"serve", "ioa serve"} {
		command := command
		alias := &struct {
			Addr  string `long:"addr" description:"HTTP listen address"`
			Token string `long:"token" description:"Server access key" default-mask:"***"`
		}{}
		action := hostcli.Action{Persistent: true, Run: func(ctx context.Context, env hostcli.Environment) error {
			section := server.Section()
			registry := cfg.NewSections()
			_ = registry.Register("ioa.server", section)
			v, err := registry.Decode(server.ConfigKey, env.Config.Extensions[server.ConfigKey])
			if err != nil {
				return err
			}
			value := *v.(*server.Options)
			if alias.Addr != "" {
				value.URL = "http://" + alias.Addr
			}
			if alias.Token != "" {
				value.Token = alias.Token
			}
			return run(ctx, value, env)
		}}
		if err := reg.Command("ioa.server", command, "Run the standalone IOA server", alias, action); err != nil {
			return err
		}
		if err := reg.Group("ioa.server", command, server.ConfigKey, cfg.FlagGroup{Name: "IOA server", Options: &server.Options{}}); err != nil {
			return err
		}
	}
	return nil
}
