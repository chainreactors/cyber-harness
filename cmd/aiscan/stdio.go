package main

import (
	"context"
	"fmt"
	"io"

	"github.com/chainreactors/cyber/agent"
	agentsession "github.com/chainreactors/cyber/agent/session"
	aop "github.com/chainreactors/cyber/aop"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/telemetry"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/host"
	"github.com/chainreactors/cyber/pkg/profile"
)

// RunStdio assembles a Profile around the transport-only host.
func runStdio(ctx context.Context, newProfile func(profile.Request) (profile.Profile, error), option *cfg.Option, logger telemetry.Logger, input io.Reader, output io.Writer) (runErr error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	p, rt, err := loadAgentProfile(ctx, newProfile, option, logger, &agentsession.Config{Loop: agent.StandardLoop{}})
	if err != nil {
		return err
	}
	mux := aop.NewNamespaceMux(ctx)
	if err := p.RegisterNamespaces(mux); err != nil {
		_ = p.Close(context.Background())
		return err
	}
	h := host.New(mux)
	stream := host.NewStdio(input, output)
	unsubscribe := rt.Observe(coreevents.ObserverFunc(func(event *aop.Event) {
		_ = h.Send(aop.Reply("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{Event: event}}), stream.Send)
	}))
	// One owner closes in dependency order and checks failures from the last
	// session-ended events as well as ordinary replies.
	defer func() {
		_ = p.Close(context.Background())
		unsubscribe.Cancel()
		h.Close()
		if err := h.Err(); err != nil {
			runErr = fmt.Errorf("write stdio protocol: %w", err)
		}
	}()
	err = h.Serve(stream)
	if err != nil {
		cancel()
	}
	// EOF drains admitted work. Keep events subscribed through session closure,
	// then release this connection's listener before checking all write errors.
	rt.WaitOperations()
	if err != nil {
		return fmt.Errorf("stdio protocol: %w", err)
	}
	return nil
}
