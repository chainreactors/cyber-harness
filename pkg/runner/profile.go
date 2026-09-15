package runner

import (
	"context"

	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/telemetry"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	agentext "github.com/chainreactors/cyber/pkg/exts/session"
	profile "github.com/chainreactors/cyber/pkg/profile"
)

func loadAgentProfile(ctx context.Context, factory profile.Factory, option *cfg.Option, logger telemetry.Logger, runtimeConfig *agentext.Config) (profile.Application, *agentext.Runtime, error) {
	product, err := factory.Build(profile.Request{
		Option: option,
		Features: apppkg.RuntimeFeatures{
			ProviderEnabled: true,
			ToolsEnabled:    true, AIEnabled: true,
		},
		Runtime: runtimeConfig,
		Logger:  logger,
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
