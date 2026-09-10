package console

import (
	"context"

	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	runtimepkg "github.com/chainreactors/aiscan/pkg/runtime"
)

// RunTask owns static presentation and its event subscription. Runtime only
// executes the session and publishes events; Console owns presentation.
func RunTask(ctx context.Context, rt *runtimepkg.AgentRuntime, option *cfg.Option, sessionID, label, display string, input runtimepkg.RunInput) error {
	session, err := rt.OpenSession(ctx, runtimepkg.SessionOptions{ID: sessionID})
	if err != nil {
		return err
	}
	output := NewStaticAgentOutput(option)
	defer output.Close()
	unsubscribe := rt.Subscribe(func(event *aop.Event) {
		if event != nil && event.SessionId == session.ID() && !isSessionBootstrapEvent(event) {
			output.HandleEvent(event)
		}
	})
	defer unsubscribe()
	output.Start(label, display)
	defer rt.CloseSession(context.Background(), sessionID, runtimepkg.SessionCloseCompleted)
	run, err := session.Run(ctx, input)
	if err != nil {
		return err
	}
	_, err = run.Wait()
	return err
}
