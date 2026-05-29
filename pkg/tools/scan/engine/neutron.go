package engine

import (
	"context"
	"errors"
	"fmt"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/neutron/common"
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
	Debug        bool
}

func NeutronExecuteStream(ctx context.Context, eng *neutron.Engine, index *association.Index, opts NeutronExecuteOptions) (<-chan *neutron.ExecuteResult, error) {
	if eng == nil {
		return nil, fmt.Errorf("neutron engine is not available")
	}
	if opts.Debug {
		common.NeutronLog = telemetry.EnableLogsDebug()
	} else {
		common.NeutronLog = telemetry.GlobalLogs()
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

// FingerAllowedIDs builds the set of template IDs allowed by the given
// fingerprints and index. This is shared between the scan pipeline and
// the standalone neutron command.
func FingerAllowedIDs(index *association.Index, fingers []string, maxPerFinger int) map[string]struct{} {
	allowed := make(map[string]struct{})
	if index == nil {
		return allowed
	}
	for _, finger := range fingers {
		result := index.Lookup(association.NewQuery().WithFingers(finger))
		if result == nil {
			continue
		}
		tpls := result.Templates
		if maxPerFinger > 0 && len(tpls) > maxPerFinger {
			tpls = tpls[:maxPerFinger]
		}
		for _, tpl := range tpls {
			if tpl != nil && tpl.Id != "" {
				allowed[tpl.Id] = struct{}{}
			}
		}
	}
	return allowed
}

func SelectNeutronTemplates(eng *neutron.Engine, index *association.Index, opts NeutronExecuteOptions) ([]*templates.Template, bool) {
	if len(opts.Fingers) == 0 {
		if opts.Broad {
			return nil, false
		}
		return nil, true
	}
	if eng == nil {
		return nil, true
	}

	allowedByFinger := FingerAllowedIDs(index, opts.Fingers, opts.MaxPerFinger)
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
