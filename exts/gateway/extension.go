package gateway

import (
	"context"
	"errors"
	"fmt"
	"slices"
	"sort"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/resource"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

// Kind binds a configuration schema to an existing service owner. Use selects
// a named instance; an omitted selection requires at most one matching service.
// The owner calls its upstream API directly; the binding has no HTTP listener.
// Load receives nil for an explicitly disabled service, and is not called for
// an absent service. Its returned handle owns only work started by this binding.
type Kind struct {
	Config cfg.Section
	Use    string
	Load   func(*extension.Scope, any) (resource.Handle, error)
}

type Extension struct {
	values  cfg.Values
	kinds   []Kind
	handles []resource.Handle
}

func New(values cfg.Values, kinds ...Kind) *Extension {
	return &Extension{values: cfg.CloneValues(values), kinds: append([]Kind(nil), kinds...)}
}

func (e *Extension) Load(scope *extension.Scope) error {
	schemas := cfg.NewSections()
	for _, kind := range e.kinds {
		if kind.Load == nil {
			return fmt.Errorf("gateway kind %q requires a binding", kind.Config.Key)
		}
		if _, err := schemas.Add(kind.Config); err != nil {
			return err
		}
	}
	schemas.Seal()
	// Validate the entire snapshot before configuring any consumer.
	decoded := map[string]any{}
	byKind := map[string][]string{}
	for name, fields := range e.values {
		kind, value, err := decode(schemas, name, fields)
		if err != nil {
			return err
		}
		decoded[name] = value
		byKind[kind] = append(byKind[kind], name)
	}
	selected := make([]string, len(e.kinds))
	for i, kind := range e.kinds {
		names := byKind[kind.Config.Key]
		sort.Strings(names)
		if kind.Use != "" {
			if !slices.Contains(names, kind.Use) {
				return fmt.Errorf("gateway: %s service %q is not configured", kind.Config.Key, kind.Use)
			}
			selected[i] = kind.Use
		} else if len(names) == 1 {
			selected[i] = names[0]
		} else if len(names) > 1 {
			return fmt.Errorf("gateway: select a %s service from %v", kind.Config.Key, names)
		}
	}
	for i, name := range selected {
		if name == "" {
			continue
		}
		if err := scope.Init().Err(); err != nil {
			return err
		}
		handle, err := e.kinds[i].Load(scope, decoded[name])
		// Retain partial resources too: Set will close us after a failed Load.
		if handle != nil {
			e.handles = append(e.handles, handle)
		}
		if err != nil {
			return fmt.Errorf("configure gateway.%s: %w", name, err)
		}
	}
	return nil
}

func (e *Extension) Close(ctx context.Context) error {
	var errs []error
	for len(e.handles) > 0 {
		i := len(e.handles) - 1
		err := e.handles[i].Close(ctx)
		errs = append(errs, err)
		if errors.Is(err, extension.ErrCloseIncomplete) || errors.Is(err, context.DeadlineExceeded) || errors.Is(err, context.Canceled) {
			return errors.Join(errs...)
		}
		e.handles = e.handles[:i]
	}
	return errors.Join(errs...)
}
