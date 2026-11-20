package config

import (
	"context"

	"github.com/chainreactors/cyber/core/resource"
	types "github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func Declare(resources *resource.Registry) error {
	if _, err := resource.Add[cfg.Section](resources, Section()); err != nil {
		return err
	}
	_, err := resource.Add[cfg.Connection](resources,
		cfg.Connection{Section: ConfigKey, Test: testReconConnections},
		cfg.Connection{Section: "gateway.hunter", Test: testReconConnections},
		cfg.Connection{Section: "gateway.fofa", Test: func(ctx context.Context, in, stored *types.DistributeConfig) []*types.ConnectionCheck {
			checks, _ := testGatewayConnections(ctx, in, stored, "fofa")
			return checks
		}})
	return err
}
