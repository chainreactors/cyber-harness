package service

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/extension"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	profile "github.com/chainreactors/aiscan/pkg/profile"
	web "github.com/chainreactors/aiscan/pkg/web"
	managementapi "github.com/chainreactors/aiscan/pkg/web/api"
)

func (s *Service) aiAvailable() bool {
	app, release := s.acquireApp()
	defer release()
	if app == nil {
		return false
	}
	provider, _ := app.ProviderState()
	return provider != nil
}

func (s *Service) acquireApp() (*apppkg.App, func()) {
	if s == nil {
		return nil, func() {}
	}
	s.appMu.Lock()
	p := s.profile
	if p == nil {
		s.appMu.Unlock()
		return nil, func() {}
	}
	app, err := p.App()
	if err != nil {
		s.appMu.Unlock()
		return nil, func() {}
	}
	s.profiles[p]++
	s.appMu.Unlock()

	var once sync.Once
	return app, func() {
		once.Do(func() {
			s.appMu.Lock()
			s.profiles[p]--
			retired := p != s.profile && s.profiles[p] == 0
			s.applicationChangedLocked()
			s.appMu.Unlock()
			if retired {
				ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
				defer cancel()
				// Incomplete cleanup stays in profiles for Service.Close.
				_ = s.closeApplication(ctx, p)
			}
		})
	}
}

// swapProfile transfers ownership only after validation. Retirement errors are
// retained by Service; they do not undo publication of a new profile.
func (s *Service) swapProfile(next *profile.Profile) error {
	if s == nil || next == nil {
		return fmt.Errorf("service and profile are required")
	}
	if _, err := next.App(); err != nil {
		return err
	}
	s.appMu.Lock()
	if s.closing {
		s.appMu.Unlock()
		return fmt.Errorf("service is closing")
	}
	prev := s.profile
	if prev == next {
		s.appMu.Unlock()
		return nil
	}
	if _, owned := s.profiles[next]; owned {
		s.appMu.Unlock()
		return fmt.Errorf("profile is already retiring")
	}
	s.profile = next
	s.profiles[next] = 0
	s.applicationChangedLocked()
	s.appMu.Unlock()
	if prev != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		_ = s.closeApplication(ctx, prev)
	}
	return nil
}

func (s *Service) applicationChangedLocked() {
	close(s.appChanged)
	s.appChanged = make(chan struct{})
}

func (s *Service) closeApplication(ctx context.Context, p *profile.Profile) error {
	select {
	case s.profileClose <- struct{}{}:
		defer func() { <-s.profileClose }()
	case <-ctx.Done():
		return errors.Join(extension.ErrCloseIncomplete, ctx.Err())
	}
	s.appMu.Lock()
	refs, owned := s.profiles[p]
	ready := owned && p != s.profile && refs == 0
	s.appMu.Unlock()
	if !ready {
		return nil
	}
	err := p.Close(ctx)
	s.appMu.Lock()
	if !errors.Is(err, extension.ErrCloseIncomplete) {
		delete(s.profiles, p)
		s.appError = errors.Join(s.appError, err)
	}
	s.applicationChangedLocked()
	s.appMu.Unlock()
	if errors.Is(err, extension.ErrCloseIncomplete) {
		return err
	}
	return nil
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
