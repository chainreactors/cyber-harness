package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	hostcli "github.com/chainreactors/cyber/pkg/cli"
	ioaclient "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	ioaserver "github.com/chainreactors/cyber/pkg/exts/ioa/server"
	ioatools "github.com/chainreactors/cyber/tools/ioa"
	service "github.com/chainreactors/cyber/tools/ioa/server"
)

// Query commands own only the IOA resource; they do not install Agent hooks.
func runIOAClientCommand(ctx context.Context, mode string, option ioaclient.Options, output ioaclient.ConsoleOptions, args ioaclient.ConsoleArgs, env hostcli.Environment) (resultErr error) {
	ioaURL := option.URL
	if ioaURL == "" {
		ioaURL = "http://127.0.0.1:8765"
	}
	parsed, err := url.Parse(ioaURL)
	if err != nil {
		return err
	}
	autoRegister := parsed.User != nil && parsed.User.Username() != ""
	connection := ioaclient.New(ioatools.Config{URL: ioaURL, NodeName: "cyber-cli", AutoRegister: autoRegister})
	set, err := extension.New(extension.Provided(telemetry.NewLoggerRef(env.Logger)), connection)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, set.Close(context.Background())) }()
	ctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	if err := set.Load(ctx); err != nil {
		return err
	}
	switch mode {
	case "spaces":
		return ioaclient.RunIOASpaces(ctx, connection.Service(), &output, env.Out, env.Err)
	case "nodes":
		return ioaclient.RunIOANodes(ctx, connection.Service(), &output, args, env.Out, env.Err)
	case "messages":
		if args.Space == "" {
			return fmt.Errorf("space is required")
		}
		return ioaclient.RunIOAMessages(ctx, connection.Service(), &output, args, env.Out, env.Err)
	case "context":
		if args.Space == "" || args.MessageID == "" {
			return fmt.Errorf("space and message ID are required")
		}
		return ioaclient.RunIOAContext(ctx, connection.Service(), &output, args, env.Out, env.Err)
	}
	return fmt.Errorf("unknown query %s", mode)
}

func runIOAServe(ctx context.Context, option ioaserver.Options, logger telemetry.Logger) (resultErr error) {
	listenURL := option.URL
	if listenURL == "" {
		listenURL = "http://127.0.0.1:8765"
	}
	parsed, err := url.Parse(listenURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("invalid IOA listen URL")
	}
	server := ioaserver.New(service.Config{AccessKey: option.Token, MCP: true})
	set, err := extension.New(server)
	if err != nil {
		return err
	}
	defer func() { resultErr = errors.Join(resultErr, set.Close(context.Background())) }()
	if err := set.Load(ctx); err != nil {
		return err
	}
	listener, err := net.Listen("tcp", parsed.Host)
	if err != nil {
		return err
	}
	defer listener.Close()
	logger.Importantf("aiscan server store=memory")
	logger.Infof("  agent IOA connect: aiscan agent --transport local --ioa-url http://%s@%s", server.Server().AccessKey(), listener.Addr())
	return serveManagedHTTP(ctx, &http.Server{Handler: server.Server().Handler()}, listener, set.Close)
}
