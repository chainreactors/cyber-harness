package probe

import (
	"context"
	"fmt"
	types "github.com/chainreactors/aiscan/pkg/types"
	"strings"
)

type Check func(context.Context, *types.DistributeConfig, *types.DistributeConfig) []*types.ConnectionCheck
type Registry struct{ checks map[string]Check }

func New() *Registry {
	return &Registry{checks: map[string]Check{"cyberhub": testCyberhub, "recon": testRecon, "search": testSearch}}
}
func (r *Registry) Register(section string, check Check) error {
	if section == "" || check == nil {
		return fmt.Errorf("probe requires section and check")
	}
	if _, exists := r.checks[section]; exists {
		return fmt.Errorf("duplicate probe %q", section)
	}
	r.checks[section] = check
	return nil
}
func (r *Registry) Test(ctx context.Context, section string, in, stored *types.DistributeConfig) ([]*types.ConnectionCheck, error) {
	check := r.checks[strings.ToLower(strings.TrimSpace(section))]
	if check == nil {
		return nil, fmt.Errorf("section %q has no connection to test", section)
	}
	return check(ctx, in, stored), nil
}
