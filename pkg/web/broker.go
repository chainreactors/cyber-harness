package web

import (
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
	scanpb "github.com/chainreactors/aiscan/pkg/types/scan"
	protobuf "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// Hub is a typed in-process broker. Durable replay remains the responsibility
// of the event stores; live protobuf values never pass through JSON envelopes.
type Hub struct {
	mu              sync.Mutex
	aopSubscribers  map[string]map[chan AOPDelivery]struct{}
	scanSubscribers map[string]map[chan *scanpb.ScanEvent]struct{}
	scanSequence    map[string]uint64
}

type AOPDelivery struct {
	Cursor int64
	Event  *aop.Event
}

func NewHub() *Hub {
	return &Hub{
		aopSubscribers:  make(map[string]map[chan AOPDelivery]struct{}),
		scanSubscribers: make(map[string]map[chan *scanpb.ScanEvent]struct{}),
		scanSequence:    make(map[string]uint64),
	}
}

func (h *Hub) SubscribeAOP(sessionID string) (<-chan AOPDelivery, func()) {
	ch := make(chan AOPDelivery, 64)
	h.mu.Lock()
	if _, ok := h.aopSubscribers[sessionID]; !ok {
		h.aopSubscribers[sessionID] = make(map[chan AOPDelivery]struct{})
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

func (h *Hub) BroadcastAOP(sessionID string, delivery AOPDelivery, reliable bool) {
	if delivery.Event == nil {
		return
	}
	h.mu.Lock()
	for ch := range h.aopSubscribers[sessionID] {
		value := AOPDelivery{Cursor: delivery.Cursor, Event: protobuf.Clone(delivery.Event).(*aop.Event)}
		broadcastBuffered(ch, value, reliable)
	}
	h.mu.Unlock()
}

// SubscribeScan registers a live subscriber and returns the sequence that was
// current at the subscription boundary. A caller can stamp its initial
// snapshot with this value, then safely ignore queued events at or below it.
func (h *Hub) SubscribeScan(scanID string) (<-chan *scanpb.ScanEvent, uint64, func()) {
	ch := make(chan *scanpb.ScanEvent, 64)
	h.mu.Lock()
	if _, ok := h.scanSubscribers[scanID]; !ok {
		h.scanSubscribers[scanID] = make(map[chan *scanpb.ScanEvent]struct{})
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

func (h *Hub) BroadcastScan(event *scanpb.ScanEvent, reliable bool) {
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
		broadcastBuffered(ch, protobuf.Clone(event).(*scanpb.ScanEvent), reliable)
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
