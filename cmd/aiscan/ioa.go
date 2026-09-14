package main

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"time"

	"github.com/chainreactors/aiscan/core/extension"
	"github.com/chainreactors/aiscan/core/telemetry"
	hostcli "github.com/chainreactors/aiscan/pkg/cli"
	clientext "github.com/chainreactors/aiscan/pkg/exts/ioa/client"
	presentation "github.com/chainreactors/aiscan/pkg/exts/ioa/client/console"
	serverext "github.com/chainreactors/aiscan/pkg/exts/ioa/server"
	ioatools "github.com/chainreactors/aiscan/tools/ioa"
	service "github.com/chainreactors/aiscan/tools/ioa/server"
)

// The query CLI owns a client-only graph with no Agent, inbox, or event output.
func runIOAClientCommand(ctx context.Context, mode string, option clientext.Options, output presentation.Options, args presentation.Args, env hostcli.Environment) (resultErr error) {
	ioaURL := option.URL
	if ioaURL == "" {
		ioaURL = "http://127.0.0.1:8765"
	}
	parsed, err := url.Parse(ioaURL)
	if err != nil {
		return err
	}
	autoRegister := parsed.User != nil && parsed.User.Username() != ""
	client, err := clientext.New(ioatools.Config{URL: ioaURL, NodeName: "aiscan-cli", AutoRegister: autoRegister}, clientext.Services{Logger: env.Logger})
	if err != nil {
		return err
	}
	set, err := extension.New(extension.Entry{ID: ioaID, Extension: client})
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
		return presentation.RunIOASpaces(ctx, client.Runtime(), &output, env.Out, env.Err)
	case "nodes":
		return presentation.RunIOANodes(ctx, client.Runtime(), &output, args, env.Out, env.Err)
	case "messages":
		if args.Space == "" {
			return fmt.Errorf("space is required")
		}
		return presentation.RunIOAMessages(ctx, client.Runtime(), &output, args, env.Out, env.Err)
	case "context":
		if args.Space == "" || args.MessageID == "" {
			return fmt.Errorf("space and message ID are required")
		}
		return presentation.RunIOAContext(ctx, client.Runtime(), &output, args, env.Out, env.Err)
	}
	return fmt.Errorf("unknown query %s", mode)
}

func runIOAServe(ctx context.Context, option serverext.Options, logger telemetry.Logger) (resultErr error) {
	listenURL := option.URL
	if listenURL == "" {
		listenURL = "http://127.0.0.1:8765"
	}
	parsed, err := url.Parse(listenURL)
	if err != nil || parsed.Host == "" || (parsed.Scheme != "http" && parsed.Scheme != "https") {
		return fmt.Errorf("invalid IOA listen URL")
	}
	server := serverext.New(service.Config{AccessKey: option.Token, MCP: true})
	set, err := extension.New(extension.Entry{ID: "ioa-server", Extension: server})
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
