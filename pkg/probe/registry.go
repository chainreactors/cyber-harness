package probe

import (
	"context"
	"fmt"
	types "github.com/chainreactors/cyber/pkg/types"
	"strings"
)

type Check func(context.Context, *types.DistributeConfig, *types.DistributeConfig) []*types.ConnectionCheck
type entry struct {
	source string
	check  Check
}
type Registry struct {
	checks map[string]entry
	sealed bool
}

func New() *Registry {
	return &Registry{checks: map[string]entry{}}
}
func (r *Registry) Register(source, section string, check Check) error {
	if r.sealed {
		return fmt.Errorf("probe declarations are sealed")
	}
	if strings.TrimSpace(source) == "" || section == "" || section != strings.ToLower(strings.TrimSpace(section)) || check == nil {
		return fmt.Errorf("probe requires section and check")
	}
	if previous, exists := r.checks[section]; exists {
		return fmt.Errorf("duplicate probe %q (sources %s and %s)", section, previous.source, source)
	}
	r.checks[section] = entry{source, check}
	return nil
}
func (r *Registry) Test(ctx context.Context, section string, in, stored *types.DistributeConfig) ([]*types.ConnectionCheck, error) {
	value := r.checks[strings.ToLower(strings.TrimSpace(section))]
	if value.check == nil {
		return nil, fmt.Errorf("section %q has no connection to test", section)
	}
	return value.check(ctx, in, stored), nil
}

func (r *Registry) Seal() { r.sealed = true }
