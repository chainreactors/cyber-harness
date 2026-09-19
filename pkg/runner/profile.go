package runner

import (
	"context"
	"fmt"

	agentsession "github.com/chainreactors/cyber/agent/session"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/telemetry"
	profile "github.com/chainreactors/cyber/pkg/profile"
)

func loadAgentProfile(ctx context.Context, newProfile func(profile.Request) (profile.Profile, error), option *cfg.Option, logger telemetry.Logger, sessionConfig *agentsession.Config) (profile.Profile, *agentsession.Runtime, error) {
	if newProfile == nil {
		return nil, nil, fmt.Errorf("profile constructor is required")
	}
	p, err := newProfile(profile.Request{
		Option:       option,
		ProviderMode: profile.ProviderRequired,
		Session:      sessionConfig,
		Logger:       logger,
	})
	if err != nil {
		return nil, nil, err
	}
	if p == nil {
		return nil, nil, fmt.Errorf("profile constructor returned nil")
	}
	if err := p.Load(ctx); err != nil {
		_ = p.Close(context.Background())
		return nil, nil, err
	}
	run, err := p.Runtime()
	if err != nil {
		_ = p.Close(context.Background())
		return nil, nil, err
	}
	return p, run, nil
}
