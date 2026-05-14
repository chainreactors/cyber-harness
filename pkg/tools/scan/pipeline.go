package scan

import (
	"context"
	"fmt"
	"os"
	"sync"
)

type pipelineEventKind string

const (
	pipelineEventEmit            pipelineEventKind = "emit"
	pipelineEventAccept          pipelineEventKind = "accept"
	pipelineEventDispatch        pipelineEventKind = "dispatch"
	pipelineEventDedupEvent      pipelineEventKind = "dedup event"
	pipelineEventDedupRun        pipelineEventKind = "dedup run"
	pipelineEventCapabilityStart pipelineEventKind = "capability start"
	pipelineEventCapabilityDone  pipelineEventKind = "capability done"
)

type pipelineEvent struct {
	Action     pipelineEventKind
	Capability string
	Event      event
}

type eventSink interface {
	Observe(pipelineEvent)
}

type pipeline struct {
	ctx            context.Context
	capabilities   []capability
	sink           eventSink
	events         chan event
	queues         map[string]chan event
	dispatcherDone chan struct{}
	workersDone    sync.WaitGroup
	mu             sync.Mutex
	cond           *sync.Cond
	pending        int
	seenEvents     map[string]struct{}
	seenRuns       map[string]struct{}
	debug          bool
}

func newPipeline(ctx context.Context, capabilities []capability, sink eventSink, debug bool) *pipeline {
	p := &pipeline{
		ctx:            ctx,
		capabilities:   capabilities,
		sink:           sink,
		events:         make(chan event, 1024),
		queues:         make(map[string]chan event, len(capabilities)),
		dispatcherDone: make(chan struct{}),
		seenEvents:     make(map[string]struct{}),
		seenRuns:       make(map[string]struct{}),
		debug:          debug,
	}
	p.cond = sync.NewCond(&p.mu)
	return p
}

func (p *pipeline) Run(seeds []event) {
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

func (p *pipeline) start() {
	for _, cap := range p.capabilities {
		workers := cap.Worker
		if workers <= 0 {
			workers = 1
		}
		queue := make(chan event, 256)
		p.queues[cap.Name] = queue
		for i := 0; i < workers; i++ {
			p.workersDone.Add(1)
			go func(cap capability, queue <-chan event) {
				defer p.workersDone.Done()
				for input := range queue {
					p.observe(pipelineEventCapabilityStart, cap.Name, input)
					cap.Run(p.ctx, input, p.Submit)
					p.observe(pipelineEventCapabilityDone, cap.Name, input)
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

func (p *pipeline) Submit(event event) {
	if event.Kind == "" {
		return
	}
	p.observe(pipelineEventEmit, "", event)
	p.add()
	select {
	case p.events <- event:
	case <-p.ctx.Done():
		p.done()
	}
}

func (p *pipeline) dispatch(event event) {
	key := event.key()
	if key == "" {
		return
	}

	p.mu.Lock()
	if _, ok := p.seenEvents[key]; ok {
		p.mu.Unlock()
		p.observe(pipelineEventDedupEvent, "", event)
		return
	}
	p.seenEvents[key] = struct{}{}
	p.mu.Unlock()

	p.observe(pipelineEventAccept, "", event)
	for _, cap := range p.capabilities {
		if cap.Accept == nil || !cap.Accept(event) {
			continue
		}
		runKey := cap.keyFor(event)
		if runKey == "" {
			continue
		}
		if !p.markRun(runKey) {
			p.observe(pipelineEventDedupRun, cap.Name, event)
			continue
		}
		p.observe(pipelineEventDispatch, cap.Name, event)
		p.add()
		select {
		case p.queues[cap.Name] <- event:
		case <-p.ctx.Done():
			p.done()
		}
	}
}

func (p *pipeline) add() {
	p.mu.Lock()
	p.pending++
	p.mu.Unlock()
}

func (p *pipeline) done() {
	p.mu.Lock()
	p.pending--
	if p.pending == 0 {
		p.cond.Broadcast()
	}
	p.mu.Unlock()
}

func (p *pipeline) waitIdle() {
	p.mu.Lock()
	for p.pending > 0 {
		p.cond.Wait()
	}
	p.mu.Unlock()
}

func (p *pipeline) markRun(key string) bool {
	p.mu.Lock()
	defer p.mu.Unlock()
	if _, ok := p.seenRuns[key]; ok {
		return false
	}
	p.seenRuns[key] = struct{}{}
	return true
}

func (p *pipeline) observe(action pipelineEventKind, capability string, event event) {
	pipelineEvent := pipelineEvent{Action: action, Capability: capability, Event: event}
	if p.sink != nil {
		p.sink.Observe(pipelineEvent)
	}
	if p.debug {
		fmt.Fprintln(os.Stderr, formatTraceEvent(pipelineEvent))
	}
}
