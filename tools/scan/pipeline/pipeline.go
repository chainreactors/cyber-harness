package pipeline

import (
	"context"
	"fmt"
	"sync"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

type Event interface {
	Key() string
}

type ActionKind string

const (
	ActionEmit            ActionKind = "emit"
	ActionAccept          ActionKind = "accept"
	ActionDispatch        ActionKind = "dispatch"
	ActionDedupRoute      ActionKind = "dedup route"
	ActionCapabilityStart ActionKind = "capability start"
	ActionCapabilityDone  ActionKind = "capability done"
)

type Observation struct {
	Action     ActionKind
	Capability string
	Event      Event
}

type Route struct {
	From   string
	Accept func(Event) bool
}

type Capability struct {
	Name   string
	Routes []Route
	Worker int
	RunKey func(Event) string
	Run    func(ctx context.Context, event Event, emit func(Event))
}

func (c Capability) KeyFor(e Event) string {
	if c.RunKey != nil {
		return c.RunKey(e)
	}
	return c.Name + "|" + e.Key()
}

type Config struct {
	Capabilities []Capability
	Bus          *eventbus.Bus[Observation]
}

type routedEvent struct {
	event  Event
	source string
}

type routeEntry struct {
	from   string
	cap    *Capability
	accept func(Event) bool
	mu     sync.Mutex
	seen   map[string]struct{}
}

type Pipeline struct {
	ctx            context.Context
	capabilities   []Capability
	bus            *eventbus.Bus[Observation]
	events         chan routedEvent
	queues         map[string]chan Event
	routes         map[string][]*routeEntry
	dispatcherDone chan struct{}
	workersDone    sync.WaitGroup
	mu             sync.Mutex
	cond           *sync.Cond
	pending        int
}

// RouteStats returns per-route dedup map sizes. Keyed by "from->cap".
// A value of -1 means the map has been freed. For testing only.
func (p *Pipeline) RouteStats() map[string]int {
	stats := make(map[string]int)
	for source, entries := range p.routes {
		for _, entry := range entries {
			key := source + "->" + entry.cap.Name
			entry.mu.Lock()
			if entry.seen != nil {
				stats[key] = len(entry.seen)
			} else {
				stats[key] = -1
			}
			entry.mu.Unlock()
		}
	}
	return stats
}

const seedSource = ""

func New(ctx context.Context, cfg Config) (*Pipeline, error) {
	bus := cfg.Bus
	if bus == nil {
		bus = eventbus.New[Observation]()
	}

	if err := validateDAG(cfg.Capabilities); err != nil {
		return nil, err
	}

	p := &Pipeline{
		ctx:            ctx,
		capabilities:   cfg.Capabilities,
		bus:            bus,
		events:         make(chan routedEvent, 1024),
		queues:         make(map[string]chan Event, len(cfg.Capabilities)),
		routes:         make(map[string][]*routeEntry),
		dispatcherDone: make(chan struct{}),
	}
	p.cond = sync.NewCond(&p.mu)

	for i := range cfg.Capabilities {
		cap := &cfg.Capabilities[i]
		for _, route := range cap.Routes {
			entry := &routeEntry{
				from:   route.From,
				cap:    cap,
				accept: route.Accept,
				seen:   make(map[string]struct{}),
			}
			p.routes[route.From] = append(p.routes[route.From], entry)
		}
	}

	return p, nil
}

func (p *Pipeline) Run(seeds []Event) {
	p.start()
	for _, seed := range seeds {
		p.submit(seed, seedSource)
	}
	p.waitIdle()
	close(p.events)
	<-p.dispatcherDone
	for _, queue := range p.queues {
		close(queue)
	}
	p.workersDone.Wait()
	p.cleanup()
}

func (p *Pipeline) Submit(e Event) {
	p.submit(e, seedSource)
}

func (p *Pipeline) cleanup() {
	for _, entries := range p.routes {
		for _, entry := range entries {
			entry.mu.Lock()
			clear(entry.seen)
			entry.seen = nil
			entry.mu.Unlock()
		}
	}
	p.routes = nil
	p.queues = nil
}

func (p *Pipeline) start() {
	for i := range p.capabilities {
		cap := &p.capabilities[i]
		workers := cap.Worker
		if workers <= 0 {
			workers = 1
		}
		queue := make(chan Event, 256)
		p.queues[cap.Name] = queue
		for j := 0; j < workers; j++ {
			p.workersDone.Add(1)
			go func(cap *Capability, queue <-chan Event) {
				defer p.workersDone.Done()
				for input := range queue {
					telemetry.SafeRun("pipeline."+cap.Name, func() {
						p.emit(ActionCapabilityStart, cap.Name, input)
						cap.Run(p.ctx, input, func(e Event) {
							p.submit(e, cap.Name)
						})
						p.emit(ActionCapabilityDone, cap.Name, input)
					})
					p.done()
				}
			}(cap, queue)
		}
	}

	go func() {
		defer close(p.dispatcherDone)
		for re := range p.events {
			telemetry.SafeRun("pipeline.dispatcher", func() {
				p.dispatch(re)
			})
			p.done()
		}
	}()
}

func (p *Pipeline) submit(e Event, source string) {
	if e == nil || e.Key() == "" {
		return
	}
	p.emit(ActionEmit, source, e)
	p.add()
	select {
	case p.events <- routedEvent{event: e, source: source}:
	case <-p.ctx.Done():
		p.done()
	}
}

func (p *Pipeline) dispatch(re routedEvent) {
	key := re.event.Key()
	if key == "" {
		return
	}

	dispatched := false
	matched := false
	entries := p.routes[re.source]
	for _, entry := range entries {
		if entry.accept != nil && !entry.accept(re.event) {
			continue
		}
		matched = true

		dedupKey := entry.cap.KeyFor(re.event)
		if dedupKey == "" {
			continue
		}

		entry.mu.Lock()
		if _, seen := entry.seen[dedupKey]; seen {
			entry.mu.Unlock()
			p.emit(ActionDedupRoute, entry.cap.Name, re.event)
			continue
		}
		entry.seen[dedupKey] = struct{}{}
		entry.mu.Unlock()

		if !dispatched {
			p.emit(ActionAccept, re.source, re.event)
			dispatched = true
		}
		p.emit(ActionDispatch, entry.cap.Name, re.event)
		p.add()
		select {
		case p.queues[entry.cap.Name] <- re.event:
		case <-p.ctx.Done():
			p.done()
		}
	}

	if !matched {
		p.emit(ActionAccept, re.source, re.event)
	}
}

func (p *Pipeline) add() {
	p.mu.Lock()
	p.pending++
	p.mu.Unlock()
}

func (p *Pipeline) done() {
	p.mu.Lock()
	p.pending--
	if p.pending == 0 {
		p.cond.Broadcast()
	}
	p.mu.Unlock()
}

func (p *Pipeline) waitIdle() {
	p.mu.Lock()
	for p.pending > 0 {
		p.cond.Wait()
	}
	p.mu.Unlock()
}

func (p *Pipeline) emit(action ActionKind, capability string, e Event) {
	p.bus.Emit(Observation{Action: action, Capability: capability, Event: e})
}

func validateDAG(capabilities []Capability) error {
	adj := make(map[string]map[string]struct{})
	nodes := make(map[string]struct{})
	for _, cap := range capabilities {
		nodes[cap.Name] = struct{}{}
		for _, route := range cap.Routes {
			from := route.From
			if from == seedSource {
				continue
			}
			if _, ok := adj[from]; !ok {
				adj[from] = make(map[string]struct{})
			}
			adj[from][cap.Name] = struct{}{}
		}
	}

	const (
		white = 0
		gray  = 1
		black = 2
	)
	color := make(map[string]int)

	var visit func(string) error
	visit = func(node string) error {
		color[node] = gray
		for next := range adj[node] {
			switch color[next] {
			case gray:
				return fmt.Errorf("pipeline: cycle detected involving %q -> %q", node, next)
			case white:
				if err := visit(next); err != nil {
					return err
				}
			}
		}
		color[node] = black
		return nil
	}

	for node := range nodes {
		if color[node] == white {
			if err := visit(node); err != nil {
				return err
			}
		}
	}
	return nil
}
