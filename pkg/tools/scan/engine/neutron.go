package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/chainreactors/neutron/templates"
	"github.com/chainreactors/sdk/neutron"
	"github.com/chainreactors/sdk/pkg/association"
)

var ErrNoNeutronTemplates = errors.New("no neutron templates selected")

type NeutronExecuteOptions struct {
	Target       string
	Fingers      []string
	MaxPerFinger int
	Broad        bool
}

func NeutronExecuteStream(ctx context.Context, eng *neutron.Engine, index *association.FingerPOCIndex, opts NeutronExecuteOptions) (<-chan *neutron.ExecuteResult, error) {
	if eng == nil {
		return nil, fmt.Errorf("neutron engine is not available")
	}
	task := neutron.NewExecuteTask(opts.Target)
	selected, filtered := SelectNeutronTemplates(eng, index, opts)
	if filtered {
		if len(selected) == 0 {
			return nil, ErrNoNeutronTemplates
		}
		task.Templates = selected
	}

	resultCh, err := eng.Execute(neutron.NewContext().WithContext(ctx), task)
	if err != nil {
		return nil, err
	}

	out := make(chan *neutron.ExecuteResult)
	go func() {
		defer close(out)
		for result := range resultCh {
			execResult, ok := result.(*neutron.ExecuteResult)
			if !ok {
				continue
			}
			select {
			case out <- execResult:
			case <-ctx.Done():
				return
			}
		}
	}()
	return out, nil
}

func SelectNeutronTemplates(eng *neutron.Engine, index *association.FingerPOCIndex, opts NeutronExecuteOptions) ([]*templates.Template, bool) {
	if len(opts.Fingers) == 0 {
		if opts.Broad {
			return nil, false
		}
		return nil, true
	}
	if eng == nil {
		return nil, true
	}

	allowedByFinger := make(map[string]struct{})
	if index == nil {
		return nil, true
	}
	for _, finger := range opts.Fingers {
		ids := index.GetPOCsByFinger(finger)
		if opts.MaxPerFinger > 0 && len(ids) > opts.MaxPerFinger {
			ids = ids[:opts.MaxPerFinger]
		}
		for _, id := range ids {
			allowedByFinger[id] = struct{}{}
		}
	}
	if len(allowedByFinger) == 0 {
		return nil, true
	}

	selected := make([]*templates.Template, 0)
	for _, tmpl := range eng.Get() {
		if tmpl == nil {
			continue
		}
		if _, ok := allowedByFinger[tmpl.Id]; !ok {
			continue
		}
		selected = append(selected, tmpl)
	}
	return selected, true
}
