package runner

import (
	"context"
	"fmt"
	"io"

	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/host"
	runtimepkg "github.com/chainreactors/aiscan/pkg/runtime"
)

// RunStdio assembles the product runtime around the transport-only host.
func RunStdio(ctx context.Context, option *cfg.Option, logger telemetry.Logger, input io.Reader, output io.Writer) (runErr error) {
	ctx, cancel := context.WithCancel(ctx)
	defer cancel()
	rt, err := runtimepkg.New(ctx, option, logger, &runtimepkg.RuntimeConfig{})
	if err != nil {
		return err
	}
	mux := aop.NewNamespaceMux()
	if err := rt.RegisterNamespaces(mux); err != nil {
		rt.Close()
		return err
	}
	h := host.New(ctx, mux)
	stream := host.NewStdio(input, output)
	unsubscribe := rt.Subscribe(func(event *aop.Event) {
		_ = h.Send(aop.Reply("", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{Event: event}}), stream.Send)
	})
	// One owner closes in dependency order and checks failures from the last
	// session-ended events as well as ordinary replies.
	defer func() {
		rt.Close()
		unsubscribe()
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
