package scan

import (
	"context"
	"fmt"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/tools/scan/pipeline"
)

type pipelineEvent struct {
	Action     pipeline.ActionKind
	Capability string
	Event      event
}

func subscribePipeline(bus *eventbus.Bus[pipeline.Observation], coll *collector, debug bool) {
	if coll != nil {
		bus.Subscribe(func(obs pipeline.Observation) {
			e, _ := obs.Event.(event)
			coll.Observe(pipelineEvent{Action: obs.Action, Capability: obs.Capability, Event: e})
		})
	}
	if debug {
		bus.Subscribe(func(obs pipeline.Observation) {
			e, _ := obs.Event.(event)
			if trace := formatTraceEvent(pipelineEvent{Action: obs.Action, Capability: obs.Capability, Event: e}); trace != "" {
				fmt.Fprintln(commands.Output, trace)
			}
		})
	}
}

func wrapRoutes(accept func(event) bool, sources ...string) []pipeline.Route {
	filter := func(e pipeline.Event) bool {
		se, ok := e.(event)
		return ok && accept != nil && accept(se)
	}
	routes := make([]pipeline.Route, len(sources))
	for i, src := range sources {
		routes[i] = pipeline.Route{From: src, Accept: filter}
	}
	return routes
}

func wrapCapability(name string, routes []pipeline.Route, worker int, run func(context.Context, event, func(event))) pipeline.Capability {
	return pipeline.Capability{
		Name:   name,
		Routes: routes,
		Worker: worker,
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
