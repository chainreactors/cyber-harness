package probe

import (
	"context"
	"fmt"
	"github.com/chainreactors/cyber/core/resource"
	types "github.com/chainreactors/cyber/pkg/types"
	"strings"
	"sync"
)

type Check func(context.Context, *types.DistributeConfig, *types.DistributeConfig) []*types.ConnectionCheck
type Definition struct {
	Section string
	Check   Check
}
type Registry struct {
	mu     sync.RWMutex
	checks map[string]Check
	sealed bool
}

func New() *Registry {
	return &Registry{checks: map[string]Check{}}
}

func (r *Registry) Add(values ...Definition) (resource.Handle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return nil, fmt.Errorf("probe declarations are sealed")
	}
	if len(values) == 0 {
		return nil, resource.ErrInvalid
	}
	sections := make([]string, 0, len(values))
	seen := make(map[string]bool, len(values))
	for _, value := range values {
		section := strings.TrimSpace(value.Section)
		if section == "" || section != strings.ToLower(section) || value.Check == nil {
			return nil, fmt.Errorf("probe requires section and check")
		}
		if _, exists := r.checks[section]; exists || seen[section] {
			return nil, fmt.Errorf("duplicate probe %q", section)
		}
		seen[section] = true
		sections = append(sections, section)
	}
	for index, section := range sections {
		r.checks[section] = values[index].Check
	}
	closed := false
	return resource.HandleFunc(func(context.Context) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		if closed {
			return nil
		}
		closed = true
		for _, section := range sections {
			delete(r.checks, section)
		}
		return nil
	}), nil
}
func (r *Registry) Test(ctx context.Context, section string, in, stored *types.DistributeConfig) ([]*types.ConnectionCheck, error) {
	r.mu.RLock()
	check := r.checks[strings.ToLower(strings.TrimSpace(section))]
	r.mu.RUnlock()
	if check == nil {
		return nil, fmt.Errorf("section %q has no connection to test", section)
	}
	return check(ctx, in, stored), nil
}

func (r *Registry) Seal() {
	r.mu.Lock()
	r.sealed = true
	r.mu.Unlock()
}

var _ resource.Point[Definition] = (*Registry)(nil)
