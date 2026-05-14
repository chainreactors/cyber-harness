package pipeline

import (
	"context"
	"fmt"
	"os"
	"sync"
)

type Event interface {
	Key() string
}

type ActionKind string

const (
	ActionEmit            ActionKind = "emit"
	ActionAccept          ActionKind = "accept"
	ActionDispatch        ActionKind = "dispatch"
	ActionDedupEvent      ActionKind = "dedup event"
	ActionDedupRun        ActionKind = "dedup run"
	ActionCapabilityStart ActionKind = "capability start"
	ActionCapabilityDone  ActionKind = "capability done"
)

type Observation struct {
	Action     ActionKind
	Capability string
	Event      Event
}

type Capability struct {
	Name   string
	Accept func(Event) bool
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
	Observe      func(Observation)
	Debug        func(Observation) string
}

type Pipeline struct {
	ctx            context.Context
	capabilities   []Capability
	observe        func(Observation)
	debugFn        func(Observation) string
	events         chan Event
	queues         map[string]chan Event
	dispatcherDone chan struct{}
	workersDone    sync.WaitGroup
	mu             sync.Mutex
	cond           *sync.Cond
	pending        int
	seenEvents     map[string]struct{}
	seenRuns       map[string]struct{}
}

func New(ctx context.Context, cfg Config) *Pipeline {
	p := &Pipeline{
		ctx:          ctx,
		capabilities: cfg.Capabilities,
		observe:      cfg.Observe,
		debugFn:      cfg.Debug,
		events:       make(chan Event, 1024),
		queues:       make(map[string]chan Event, len(cfg.Capabilities)),
		dispatcherDone: make(chan struct{}),
		seenEvents:   make(map[string]struct{}),
		seenRuns:     make(map[string]struct{}),
	}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *Pipeline) Run(seeds []Event) {
	p.start()
	for _, seed := range seeds {
		p.Submit(seed)
	}
	p.waitIdle()
	close(p.events)
	<-p.dispatcherDone
	for _, queue := range p.queues {
		close(queue)
	}
	p.workersDone.Wait()
}

func (p *Pipeline) start() {
	for _, cap := range p.capabilities {
		workers := cap.Worker
		if workers <= 0 {
			workers = 1
		}
		queue := make(chan Event, 256)
		p.queues[cap.Name] = queue
		for i := 0; i < workers; i++ {
			p.workersDone.Add(1)
			go func(cap Capability, queue <-chan Event) {
				defer p.workersDone.Done()
				for input := range queue {
					p.emit(ActionCapabilityStart, cap.Name, input)
					cap.Run(p.ctx, input, p.Submit)
					p.emit(ActionCapabilityDone, cap.Name, input)
					p.done()
				}
			}(cap, queue)
		}
	}

	go func() {
		defer close(p.dispatcherDone)
		for event := range p.events {
			p.dispatch(event)
			p.done()
		}
	}()
}

func (p *Pipeline) Submit(e Event) {
	if e == nil || e.Key() == "" {
		return
	}
	p.emit(ActionEmit, "", e)
	p.add()
	select {
	case p.events <- e:
	case <-p.ctx.Done():
		p.done()
	}
}

func (p *Pipeline) dispatch(e Event) {
	key := e.Key()
	if key == "" {
		return
	}

	p.mu.Lock()
	if _, ok := p.seenEvents[key]; ok {
		p.mu.Unlock()
		p.emit(ActionDedupEvent, "", e)
		return
	}
	p.seenEvents[key] = struct{}{}
	p.mu.Unlock()

	p.emit(ActionAccept, "", e)
	for _, cap := range p.capabilities {
		if cap.Accept == nil || !cap.Accept(e) {
			continue
		}
		runKey := cap.KeyFor(e)
		if runKey == "" {
			continue
		}
		if !p.markRun(runKey) {
			p.emit(ActionDedupRun, cap.Name, e)
			continue
		}
		p.emit(ActionDispatch, cap.Name, e)
		p.add()
		select {
		case p.queues[cap.Name] <- e:
		case <-p.ctx.Done():
			p.done()
		}
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

func (p *Pipeline) markRun(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.seenRuns[key]; ok {
		return false
	}
	p.seenRuns[key] = struct{}{}
	return true
}

func (p *Pipeline) emit(action ActionKind, capability string, e Event) {
	obs := Observation{Action: action, Capability: capability, Event: e}
	if p.observe != nil {
		p.observe(obs)
	}
	if p.debugFn != nil {
		fmt.Fprintln(os.Stderr, p.debugFn(obs))
	}
}
