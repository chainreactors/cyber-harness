package scan

import (
	"context"

	"github.com/chainreactors/aiscan/pkg/tools/scan/pipeline"
)

type pipelineEvent struct {
	Action     pipeline.ActionKind
	Capability string
	Event      event
}

func wrapObserve(coll *collector, debug bool) (func(pipeline.Observation), func(pipeline.Observation) string) {
	observe := func(obs pipeline.Observation) {
		if coll == nil {
			return
		}
		e, _ := obs.Event.(event)
		coll.Observe(pipelineEvent{Action: obs.Action, Capability: obs.Capability, Event: e})
	}
	var debugFn func(pipeline.Observation) string
	if debug {
		debugFn = func(obs pipeline.Observation) string {
			e, _ := obs.Event.(event)
			return formatTraceEvent(pipelineEvent{Action: obs.Action, Capability: obs.Capability, Event: e})
		}
	}
	return observe, debugFn
}

func wrapCapability(name string, accept func(event) bool, worker int, run func(context.Context, event, func(event))) pipeline.Capability {
	return pipeline.Capability{
		Name:   name,
		Worker: worker,
		Accept: func(e pipeline.Event) bool {
			se, ok := e.(event)
			return ok && accept != nil && accept(se)
		},
		Run: func(ctx context.Context, e pipeline.Event, emit func(pipeline.Event)) {
			se, ok := e.(event)
			if !ok {
				return
			}
			run(ctx, se, func(ev event) { emit(ev) })
		},
	}
}

func seedsToEvents(seeds []event) []pipeline.Event {
	out := make([]pipeline.Event, len(seeds))
	for i, s := range seeds {
		out[i] = s
	}
	return out
}
