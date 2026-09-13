package runner

import (
	"context"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	apppkg "github.com/chainreactors/aiscan/pkg/app"
	sessionext "github.com/chainreactors/aiscan/pkg/exts/session"
	profile "github.com/chainreactors/aiscan/pkg/profile/aiscan"
)

func loadAgentProfile(ctx context.Context, option *cfg.Option, logger telemetry.Logger, runtimeConfig *sessionext.Config) (*profile.Profile, *sessionext.Manager, error) {
	config := profile.FromOption(option, apppkg.RuntimeFeatures{
		ProviderEnabled: true,
		ToolsEnabled:    true, AIEnabled: true,
	}, runtimeConfig, logger)
	product, err := profile.New(config)
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
