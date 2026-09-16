package cli

import (
	"context"
	cfg "github.com/chainreactors/cyber/core/config"
	hostcli "github.com/chainreactors/cyber/pkg/cli"
	client "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	presentation "github.com/chainreactors/cyber/pkg/exts/ioa/client/console"
)

type Execute func(context.Context, string, client.Options, presentation.Options, presentation.Args, hostcli.Environment) error

func Register(reg *hostcli.Registry, run Execute) error {
	jsonOption := &struct {
		JSON bool `long:"json" description:"Output query results in JSON format"`
	}{}
	for _, mode := range []string{"spaces", "messages", "context", "nodes"} {
		mode := mode
		var args any
		var readArgs func() presentation.Args
		switch mode {
		case "spaces":
			args = &struct{}{}
			readArgs = func() presentation.Args { return presentation.Args{} }
		case "messages":
			value := &struct {
				Positional struct {
					Space string `positional-arg-name:"space"`
				} `positional-args:"yes" required:"yes"`
			}{}
			args = value
			readArgs = func() presentation.Args { return presentation.Args{Space: value.Positional.Space} }
		case "context":
			value := &struct {
				Positional struct {
					Space     string `positional-arg-name:"space"`
					MessageID string `positional-arg-name:"message-id"`
				} `positional-args:"yes" required:"yes"`
			}{}
			args = value
			readArgs = func() presentation.Args {
				return presentation.Args{Space: value.Positional.Space, MessageID: value.Positional.MessageID}
			}
		case "nodes":
			value := &struct {
				Positional struct {
					Space string `positional-arg-name:"space"`
				} `positional-args:"yes"`
			}{}
			args = value
			readArgs = func() presentation.Args { return presentation.Args{Space: value.Positional.Space} }
		}
		action := hostcli.Action{Run: func(ctx context.Context, env hostcli.Environment) error {
			option, err := client.ReadOptions(env.Config)
			if err != nil {
				return err
			}
			return run(ctx, mode, option, presentation.Options{JSON: jsonOption.JSON}, readArgs(), env)
		}}
		if err := reg.Command("ioa "+mode, "IOA "+mode, args, action); err != nil {
			return err
		}
	}
	if err := reg.Group("ioa", "", cfg.FlagGroup{Name: "Query output", Options: jsonOption}); err != nil {
		return err
	}
	if err := reg.Group("ioa", client.ConfigKey, client.FlagGroup()); err != nil {
		return err
	}
	return nil
}
