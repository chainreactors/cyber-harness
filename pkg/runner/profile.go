package runner

import (
	"context"

	agentsession "github.com/chainreactors/cyber/agent/session"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/telemetry"
	profile "github.com/chainreactors/cyber/pkg/profile"
)

func loadAgentProfile(ctx context.Context, factory profile.Factory, option *cfg.Option, logger telemetry.Logger, sessionConfig *agentsession.Config) (profile.Application, *agentsession.Runtime, error) {
	p, err := factory.Build(profile.Request{
		Option:       option,
		ProviderMode: profile.ProviderRequired,
		Session:      sessionConfig,
		Logger:       logger,
	})
	if err != nil {
		return nil, nil, err
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
