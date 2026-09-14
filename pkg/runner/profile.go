package runner

import (
	"context"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	sessionext "github.com/chainreactors/aiscan/pkg/exts/session"
	profile "github.com/chainreactors/aiscan/pkg/profile"
)

func loadAgentProfile(ctx context.Context, factory profile.Factory, option *cfg.Option, logger telemetry.Logger, runtimeConfig *sessionext.Config) (profile.Application, *sessionext.Manager, error) {
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
