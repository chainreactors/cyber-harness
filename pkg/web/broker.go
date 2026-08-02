package web

import (
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
	types "github.com/chainreactors/aiscan/pkg/types"
	protobuf "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Hub is a typed in-process broker. Durable replay remains the responsibility
// of the event stores; live protobuf values never pass through JSON envelopes.
type Hub struct {
	mu              sync.Mutex
	aopSubscribers  map[string]map[chan *aop.EventDelivery]struct{}
	scanSubscribers map[string]map[chan *types.ScanEvent]struct{}
	scanSequence    map[string]uint64
}

func NewHub() *Hub {
	return &Hub{
		aopSubscribers:  make(map[string]map[chan *aop.EventDelivery]struct{}),
		scanSubscribers: make(map[string]map[chan *types.ScanEvent]struct{}),
		scanSequence:    make(map[string]uint64),
	}
}

func (h *Hub) SubscribeAOP(sessionID string) (<-chan *aop.EventDelivery, func()) {
	ch := make(chan *aop.EventDelivery, 64)
	h.mu.Lock()
	if _, ok := h.aopSubscribers[sessionID]; !ok {
		h.aopSubscribers[sessionID] = make(map[chan *aop.EventDelivery]struct{})
	}
	h.aopSubscribers[sessionID][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if bucket, ok := h.aopSubscribers[sessionID]; ok {
			delete(bucket, ch)
			if len(bucket) == 0 {
				delete(h.aopSubscribers, sessionID)
			}
		}
		close(ch)
		h.mu.Unlock()
	}
}

func (h *Hub) BroadcastAOP(sessionID string, delivery *aop.EventDelivery, reliable bool) {
	if delivery == nil || delivery.Event == nil {
		return
	}
	h.mu.Lock()
	for ch := range h.aopSubscribers[sessionID] {
		value := protobuf.CloneOf(delivery)
		broadcastBuffered(ch, value, reliable)
	}
	h.mu.Unlock()
}

// SubscribeScan registers a live subscriber and returns the sequence that was
// current at the subscription boundary. A caller can stamp its initial
// snapshot with this value, then safely ignore queued events at or below it.
func (h *Hub) SubscribeScan(scanID string) (<-chan *types.ScanEvent, uint64, func()) {
	ch := make(chan *types.ScanEvent, 64)
	h.mu.Lock()
	if _, ok := h.scanSubscribers[scanID]; !ok {
		h.scanSubscribers[scanID] = make(map[chan *types.ScanEvent]struct{})
	}
	h.scanSubscribers[scanID][ch] = struct{}{}
	sequence := h.scanSequence[scanID]
	h.mu.Unlock()
	return ch, sequence, func() {
		h.mu.Lock()
		if bucket, ok := h.scanSubscribers[scanID]; ok {
			delete(bucket, ch)
			if len(bucket) == 0 {
				delete(h.scanSubscribers, scanID)
			}
		}
		close(ch)
		h.mu.Unlock()
	}
}

func (h *Hub) BroadcastScan(event *types.ScanEvent, reliable bool) {
	if event == nil || event.ScanId == "" {
		return
	}
	h.mu.Lock()
	if event.Sequence == 0 {
		h.scanSequence[event.ScanId]++
		event.Sequence = h.scanSequence[event.ScanId]
	} else if event.Sequence > h.scanSequence[event.ScanId] {
		h.scanSequence[event.ScanId] = event.Sequence
	}
	if event.EmittedAt == nil {
		event.EmittedAt = timestamppb.Now()
	}
	for ch := range h.scanSubscribers[event.ScanId] {
		broadcastBuffered(ch, protobuf.CloneOf(event), reliable)
	}
	h.mu.Unlock()
}

func broadcastBuffered[T any](ch chan T, value T, reliable bool) {
	select {
	case ch <- value:
	default:
		if !reliable {
			return
		}
		select {
		case <-ch:
		default:
		}
		select {
		case ch <- value:
		default:
		}
	}
}
