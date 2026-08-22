package proxy

import (
	"strconv"
	"sync"

	traffic "github.com/chainreactors/aiscan/aop/traffic"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (h *ProxyHub) Store() *FlowStore { return h.store }

func (h *ProxyHub) ingest(flow Flow) {
	if !h.recording.Load() {
		return
	}
	if !h.captureMatches(flow) {
		return
	}
	stored := h.store.Add(flow)
	h.publish(&stored)
}

func (h *ProxyHub) publish(flow *Flow) {
	if flow == nil {
		return
	}
	h.subsMu.Lock()
	for _, subscriber := range h.subs {
		// The capture path never sends into a subscriber's bounded output
		// channel. A single wake-up is enough: the subscriber owns a cursor
		// and drains every FlowStore entry after it, in order. This keeps a
		// slow Cairn connection from silently dropping observations or
		// blocking the proxy response path.
		subscriber.signal()
	}
	h.subsMu.Unlock()
}

func (h *ProxyHub) Subscribe(buffer int) (<-chan *traffic.Flow, func()) {
	return h.SubscribeFrom(h.store.Sequence(), buffer)
}

// SubscribeFrom starts a reliable FlowStore-backed subscription after the
// supplied numeric flow id. It is useful for reconnecting consumers that have
// persisted their last seen id. The normal Subscribe path starts at the
// current tail and observes only new flows.
func (h *ProxyHub) SubscribeFrom(after int, buffer int) (<-chan *traffic.Flow, func()) {
	if buffer <= 0 {
		buffer = 256
	}
	channel := make(chan *traffic.Flow, buffer)
	subscriber := &flowSubscriber{
		hub:    h,
		out:    channel,
		wake:   make(chan struct{}, 1),
		done:   make(chan struct{}),
		cursor: after,
	}
	h.subsMu.Lock()
	if h.subs == nil {
		h.subs = make(map[int]*flowSubscriber)
	}
	id := h.nextSub
	h.nextSub++
	h.subs[id] = subscriber
	h.subsMu.Unlock()
	go subscriber.run()

	var once sync.Once
	cancel := func() {
		once.Do(func() {
			h.subsMu.Lock()
			if existing, ok := h.subs[id]; ok {
				delete(h.subs, id)
				close(existing.done)
			}
			h.subsMu.Unlock()
		})
	}
	return channel, cancel
}

// flowSubscriber turns a store cursor into the historical channel API. The
// output remains bounded; back-pressure is isolated to this worker and never
// reaches the MITM request/response callbacks.
type flowSubscriber struct {
	hub    *ProxyHub
	out    chan *traffic.Flow
	wake   chan struct{}
	done   chan struct{}
	cursor int
}

func (s *flowSubscriber) signal() {
	select {
	case s.wake <- struct{}{}:
	default:
	}
}

func (s *flowSubscriber) run() {
	defer close(s.out)
	for {
		flows := s.hub.store.after(s.cursor)
		for i := range flows {
			flow := flows[i]
			message := flowToProto(&flow)
			select {
			case s.out <- message:
				s.cursor = flowSequence(flow.ID)
			case <-s.done:
				return
			}
		}
		select {
		case <-s.done:
			return
		case <-s.wake:
		}
	}
}

func flowSequence(id string) int {
	seq, err := strconv.Atoi(id)
	if err != nil || seq < 0 {
		return 0
	}
	return seq
}

// flowToProto renders a stored flow as a wire Flow: the exchange semantics go
// through the canonical Exchange, attribution (tool id, timestamp) is stamped
// on top.
func flowToProto(flow *Flow) *traffic.Flow {
	if flow == nil {
		return nil
	}
	// The hot store keeps only a preview and a file reference. A wire Flow
	// retains the historical bytes field, so hydrate only at this boundary.
	copy := *flow
	copy.Exchange = flow.Exchange.Clone()
	_ = copy.Exchange.HydrateBodies()
	message := copy.Proto()
	message.ToolId = flow.ToolID
	if !flow.Timestamp.IsZero() {
		message.Timestamp = timestamppb.New(flow.Timestamp)
	}
	return message
}
