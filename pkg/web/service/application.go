package service

import (
	"context"
	"fmt"
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/pkg/runner"
	web "github.com/chainreactors/aiscan/pkg/web"
	managementapi "github.com/chainreactors/aiscan/pkg/web/api"
)

type managedApp struct {
	app     *runner.App
	refs    int
	retired bool
	closed  bool
}

func (s *Service) aiAvailable() bool {
	app, release := s.acquireApp()
	defer release()
	return app != nil && app.Provider != nil
}

func wrapManagedApp(app *runner.App) *managedApp {
	if app == nil {
		return nil
	}
	return &managedApp{app: app}
}

func retireManagedApp(ref *managedApp) *runner.App {
	if ref == nil || ref.closed {
		return nil
	}
	ref.retired = true
	if ref.refs != 0 {
		return nil
	}
	ref.closed = true
	return ref.app
}

func (s *Service) acquireApp() (*runner.App, func()) {
	if s == nil {
		return nil, func() {}
	}
	s.appMu.Lock()
	ref := s.app
	if ref != nil && !ref.closed {
		ref.refs++
	}
	s.appMu.Unlock()
	if ref == nil || ref.closed {
		return nil, func() {}
	}

	var once sync.Once
	return ref.app, func() {
		once.Do(func() {
			var closeApp *runner.App
			s.appMu.Lock()
			ref.refs--
			if ref.refs == 0 && ref.retired && !ref.closed {
				ref.closed = true
				closeApp = ref.app
			}
			s.appMu.Unlock()
			if closeApp != nil {
				closeApp.Close()
			}
		})
	}
}

func (s *Service) swapApp(next *runner.App) {
	if s == nil || next == nil {
		return
	}
	s.appMu.Lock()
	prev := s.app
	if prev != nil && prev.app == next {
		s.appMu.Unlock()
		return
	}
	s.app = wrapManagedApp(next)
	closeApp := retireManagedApp(prev)
	s.appMu.Unlock()
	if closeApp != nil {
		closeApp.Close()
	}
}

// ServeApplication performs the Application Endpoint initialization and then
// hands the unified Connection to the api business dispatcher.
func (s *Service) ServeApplication(ctx context.Context, stream aop.EnvelopeStream) error {
	if s == nil || s.api == nil || stream == nil {
		return fmt.Errorf("application AOP stream is unavailable")
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	connection, err := web.NewConnection(ctx, stream)
	if err != nil {
		return err
	}
	defer connection.Close()

	if message, unwrapErr := aop.Unwrap(first); unwrapErr == nil {
		if core, ok := message.(*aop.ProtocolMessage); ok && core.GetAgentHello() != nil {
			protocolErr, wrapErr := aop.Wrap(generateID(), first.GetId(), &aop.ProtocolMessage{Message: &aop.ProtocolMessage_ProtocolError{ProtocolError: &aop.ProtocolError{
				Code: "WRONG_ENDPOINT", Message: "AgentHello is only accepted by the node endpoint",
			}}})
			if wrapErr == nil {
				_ = connection.Send(protocolErr)
			}
			return fmt.Errorf("AgentHello sent to application endpoint")
		}
	}

	backends := &managementapi.ApplicationBackends{
		Sessions: s.api.Sessions,
		Scans:    s.api.Scans,
		Commands: s,
		Files:    s,
		NewID:    generateID,
	}
	if s.agents != nil {
		backends.PTY = s.agents
	}
	return managementapi.ServeApplication(connection, first, backends)
}

var (
	_ managementapi.PTYRouter       = (*AgentPool)(nil)
	_ managementapi.CommandExecutor = (*Service)(nil)
	_ managementapi.FileUploader    = (*Service)(nil)
)
