package runner

import (
	"context"
	"fmt"
	"io"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	sessionext "github.com/chainreactors/aiscan/pkg/exts/session"
	"github.com/chainreactors/aiscan/pkg/host"
	"github.com/chainreactors/aiscan/pkg/profile"
)

// RunStdio assembles the product runtime around the transport-only host.
func RunStdio(ctx context.Context, factory profile.Factory, option *cfg.Option, logger telemetry.Logger, input io.Reader, output io.Writer) (runErr error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	product, rt, err := loadAgentProfile(ctx, factory, option, logger, &sessionext.Config{Loop: agent.StandardLoop{}})
	if err != nil {
		return err
	}
	mux := aop.NewNamespaceMux(ctx)
	if err := rt.RegisterNamespaces(mux); err != nil {
		_ = product.Close(context.Background())
		return err
	}
	if err := product.RegisterResourceNamespaces(mux); err != nil {
		_ = product.Close(context.Background())
		return err
	}
	h := host.New(mux)
	stream := host.NewStdio(input, output)
	unsubscribe := rt.Subscribe(func(event *aop.Event) {
		_ = h.Send(aop.Reply("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{Event: event}}), stream.Send)
	})
	// One owner closes in dependency order and checks failures from the last
	// session-ended events as well as ordinary replies.
	defer func() {
		_ = product.Close(context.Background())
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
