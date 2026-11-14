package main

import (
	"context"
	"fmt"

	provider "github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/telemetry"
	cfg "github.com/chainreactors/cyber/pkg/config"
	profile "github.com/chainreactors/cyber/pkg/profile"
)

func loadAgentProfile(ctx context.Context, newProfile func(profile.Request) (profile.Profile, error), option *cfg.Option, logger telemetry.Logger, sessionConfig *agentsession.Config) (profile.Profile, *agentsession.Runtime, error) {
	if newProfile == nil {
		return nil, nil, fmt.Errorf("profile constructor is required")
	}
	p, err := newProfile(profile.Request{
		Option:       option,
		ProviderMode: provider.StartupRequired,
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
		return nil, nil, profile.CloseOnce(ctx, p.Close)(err)
	}
	run, err := p.Runtime()
	if err != nil {
		return nil, nil, profile.CloseOnce(ctx, p.Close)(err)
	}
	return p, run, nil
}
