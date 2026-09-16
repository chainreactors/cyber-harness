package runner

import (
	"context"

	agentsession "github.com/chainreactors/cyber/agent/session"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/telemetry"
	profile "github.com/chainreactors/cyber/pkg/profile"
)

func loadAgentProfile(ctx context.Context, factory profile.Factory, option *cfg.Option, logger telemetry.Logger, sessionConfig *agentsession.Config) (profile.Application, *agentsession.Runtime, error) {
	product, err := factory.Build(profile.Request{
		Option:       option,
		ProviderMode: profile.ProviderRequired,
		Session:      sessionConfig,
		Logger:       logger,
	})
	if err != nil {
		return nil, nil, err
	}
	if err := product.Load(ctx); err != nil {
		_ = product.Close(context.Background())
		return nil, nil, err
	}
	run, err := product.Runtime()
	if err != nil {
		_ = product.Close(context.Background())
		return nil, nil, err
	}
	return product, run, nil
}
