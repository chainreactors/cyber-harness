package config

import (
	"context"
	"fmt"
	"strings"

	"github.com/chainreactors/cyber/core/resource"
	types "github.com/chainreactors/cyber/core/types"
)

// Connection is an optional connectivity check contributed by the extension
// that owns a configuration section. It is configuration behavior, not a
// separately installed runtime service.
type Connection struct {
	Section string
	Test    func(context.Context, *types.DistributeConfig, *types.DistributeConfig) []*types.ConnectionCheck
}

// ConnectionPoint exposes Connection through the same resource mechanism used
// by Section. The small adapter is required because Go cannot overload Add for
// both Point[Section] and Point[Connection] on Sections itself.
func (r *Sections) ConnectionPoint() resource.Point[Connection] {
	return connectionPoint{sections: r}
}

type connectionPoint struct{ sections *Sections }

func (p connectionPoint) Add(values ...Connection) (resource.Handle, error) {
	r := p.sections
	if r == nil || len(values) == 0 {
		return nil, resource.ErrInvalid
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return nil, fmt.Errorf("configuration declarations are sealed")
	}
	if r.connections == nil {
		r.connections = make(map[string]func(context.Context, *types.DistributeConfig, *types.DistributeConfig) []*types.ConnectionCheck)
	}
	sections := make([]string, 0, len(values))
	seen := make(map[string]struct{}, len(values))
	for _, value := range values {
		section := strings.TrimSpace(value.Section)
		if section == "" || section != strings.ToLower(section) || value.Test == nil {
			return nil, fmt.Errorf("configuration connection requires a lowercase section and test")
		}
		if _, exists := seen[section]; exists {
			return nil, fmt.Errorf("duplicate configuration connection %q", section)
		}
		if _, exists := r.connections[section]; exists {
			return nil, fmt.Errorf("duplicate configuration connection %q", section)
		}
		seen[section] = struct{}{}
		sections = append(sections, section)
	}
	for index, section := range sections {
		r.connections[section] = values[index].Test
	}
	closed := false
	return resource.HandleFunc(func(context.Context) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		if closed {
			return nil
		}
		closed = true
		if r.sealed {
			return nil
		}
		for _, section := range sections {
			delete(r.connections, section)
		}
		return nil
	}), nil
}

func (r *Sections) TestConnection(ctx context.Context, section string, incoming, stored *types.DistributeConfig) ([]*types.ConnectionCheck, error) {
	if r == nil {
		return nil, fmt.Errorf("configuration declarations are unavailable")
	}
	section = strings.ToLower(strings.TrimSpace(section))
	r.mu.Lock()
	test := r.connections[section]
	r.mu.Unlock()
	if test == nil {
		return nil, fmt.Errorf("configuration section %q has no connection to test", section)
	}
	return test(ctx, incoming, stored), nil
}

var _ resource.Point[Connection] = connectionPoint{}
