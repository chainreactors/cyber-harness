package client

import (
	"context"
	hostcli "github.com/chainreactors/cyber/pkg/cli"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

type CLIExecute func(context.Context, string, Options, ConsoleOptions, ConsoleArgs, hostcli.Environment) error

func RegisterCLI(reg *hostcli.Registry, run CLIExecute) error {
	jsonOption := &struct {
		JSON bool `long:"json" description:"Output query results in JSON format"`
	}{}
	for _, mode := range []string{"spaces", "messages", "context", "nodes"} {
		mode := mode
		var args any
		var readArgs func() ConsoleArgs
		switch mode {
		case "spaces":
			args = &struct{}{}
			readArgs = func() ConsoleArgs { return ConsoleArgs{} }
		case "messages":
			value := &struct {
				Positional struct {
					Space string `positional-arg-name:"space"`
				} `positional-args:"yes" required:"yes"`
			}{}
			args = value
			readArgs = func() ConsoleArgs { return ConsoleArgs{Space: value.Positional.Space} }
		case "context":
			value := &struct {
				Positional struct {
					Space     string `positional-arg-name:"space"`
					MessageID string `positional-arg-name:"message-id"`
				} `positional-args:"yes" required:"yes"`
			}{}
			args = value
			readArgs = func() ConsoleArgs {
				return ConsoleArgs{Space: value.Positional.Space, MessageID: value.Positional.MessageID}
			}
		case "nodes":
			value := &struct {
				Positional struct {
					Space string `positional-arg-name:"space"`
				} `positional-args:"yes"`
			}{}
			args = value
			readArgs = func() ConsoleArgs { return ConsoleArgs{Space: value.Positional.Space} }
		}
		action := hostcli.Action{Run: func(ctx context.Context, env hostcli.Environment) error {
			option, err := ReadOptions(env.Config)
			if err != nil {
				return err
			}
			return run(ctx, mode, option, ConsoleOptions{JSON: jsonOption.JSON}, readArgs(), env)
		}}
		if err := reg.Command("ioa "+mode, "IOA "+mode, args, action); err != nil {
			return err
		}
	}
	if err := reg.Group("ioa", "", cfg.FlagGroup{Name: "Query output", Options: jsonOption}); err != nil {
		return err
	}
	if err := reg.Group("ioa", ConfigKey, FlagGroup()); err != nil {
		return err
	}
	return nil
}
