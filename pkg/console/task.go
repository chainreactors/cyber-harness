package console

import (
	"context"
	"strings"

	cfg "github.com/chainreactors/aiscan/core/config"
	runtimepkg "github.com/chainreactors/aiscan/pkg/runtime"
	"github.com/chainreactors/aiscan/pkg/tui"
)

// RunTask owns static presentation and its event subscription. Runtime only
// executes the session and publishes events; it never receives an output sink.
func RunTask(ctx context.Context, rt *runtimepkg.AgentRuntime, option *cfg.Option, sessionID, label, display string, input runtimepkg.RunInput) error {
	output := tui.NewStaticAgentOutput(option)
	unsubscribe := rt.Subscribe(output.HandleEvent)
	defer unsubscribe()
	output.Start(label, display)
	session, err := rt.OpenSession(ctx, runtimepkg.SessionOptions{ID: sessionID})
	if err != nil {
		return err
	}
	defer rt.CloseSession(context.Background(), sessionID, runtimepkg.SessionCloseCompleted)
	run, err := session.Run(ctx, input)
	if err != nil {
		return err
	}
	result, err := run.Wait()
	if strings.TrimSpace(result.Output) != "" {
		output.Final(result.Output)
	}
	return err
}
