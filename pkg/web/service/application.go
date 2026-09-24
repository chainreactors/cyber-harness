package service

import (
	"context"
	"fmt"
	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/pkg/aopconn"
	profile "github.com/chainreactors/cyber/pkg/profile"
	"log/slog"
)

func (s *Service) aiAvailable() bool {
	providers := s.providers()
	if providers == nil {
		return false
	}
	active, _ := providers.Current()
	return active != nil
}
func (s *Service) providers() *provider.State {
	s.appMu.Lock()
	defer s.appMu.Unlock()
	if s.closing || s.workContext.Err() != nil || s.profile == nil {
		return nil
	}
	providers, _ := s.profile.Providers()
	return providers
}

// swapProfile runs under configGate. Stop admission before cancellation; wait
// without appMu so accepted work can finish its own cleanup.
func (s *Service) swapProfile(next profile.Profile) error {
	if s == nil || next == nil {
		return fmt.Errorf("service and profile are required")
	}
	if !next.Active() {
		return fmt.Errorf("profile is not active")
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
	s.stopWork()
	s.appMu.Unlock()
	s.work.Wait()
	if prev != nil {
		if err := prev.Close(context.Background()); err != nil {
			slog.Error("close previous profile", "error", err)
		}
	}
	s.appMu.Lock()
	s.profile = next
	s.workContext, s.stopWork = context.WithCancel(context.Background())
	s.appMu.Unlock()
	return nil
}

// ServeApplication performs Application Endpoint initialization and dispatches
// application business messages through the unified Connection.
func (s *Service) ServeApplication(ctx context.Context, stream aop.EnvelopeStream) error {
	if s == nil || s.api == nil || s.api.Sessions == nil || stream == nil {
		return fmt.Errorf("application AOP stream is unavailable")
	}
	workCtx, admitted := s.beginWork()
	if !admitted {
		return fmt.Errorf("web service is switching or closing")
	}
	defer s.work.Done()
	connection, err := aopconn.NewConnection(workCtx, stream)
	if err != nil {
		return err
	}
	stopRequest := context.AfterFunc(ctx, connection.Close)
	defer stopRequest()
	s.appMu.Lock()
	p := s.profile
	s.appMu.Unlock()
	if p == nil {
		return s.serveApplication(connection, nil)
	}
	return s.serveApplication(connection, p.RegisterNamespaces)
}
