package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"slices"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/pkg/webproto"
)

// HubEvent is the unit broadcast through the SSE hub. Type is the SSE
// event name, Data is pre-serialized JSON written directly to the stream.
type HubEvent struct {
	Type string
	Data json.RawMessage
	// Reliable marks a terminal event that Broadcast must not drop under
	// backpressure: on a full buffer it evicts the oldest queued event to seat
	// one, rather than shedding it like a token delta. See isTerminalDomainEvent
	// for which events qualify and why a lost one strands the UI.
	Reliable bool
}

type Hub struct {
	mu          sync.Mutex
	subscribers map[string]map[chan HubEvent]struct{}
}

func NewHub() *Hub {
	return &Hub{
		subscribers: make(map[string]map[chan HubEvent]struct{}),
	}
}

func (h *Hub) Subscribe(id string) (<-chan HubEvent, func()) {
	ch := make(chan HubEvent, 64)
	h.mu.Lock()
	if _, ok := h.subscribers[id]; !ok {
		h.subscribers[id] = make(map[chan HubEvent]struct{})
	}
	h.subscribers[id][ch] = struct{}{}
	h.mu.Unlock()
	return ch, func() {
		h.mu.Lock()
		if bucket, ok := h.subscribers[id]; ok {
			delete(bucket, ch)
			if len(bucket) == 0 {
				delete(h.subscribers, id)
			}
		}
		close(ch)
		h.mu.Unlock()
	}
}

func (h *Hub) Broadcast(id string, event HubEvent) {
	h.mu.Lock()
	for ch := range h.subscribers[id] {
		select {
		case ch <- event:
		default:
			// Buffer full. A non-reliable event (a token delta) is simply
			// dropped — a later cumulative delta and the final message resend the
			// same text. A reliable (terminal) event must not be the one dropped,
			// so evict the oldest queued event to make room. Safe under h.mu: no
			// other Broadcast fills this channel concurrently (so the resend is
			// guaranteed room), and unsubscribe takes h.mu before close(ch), so
			// ch is still open here.
			if event.Reliable {
				select {
				case <-ch:
				default:
				}
				select {
				case ch <- event:
				default:
				}
			}
		}
	}
	h.mu.Unlock()
}

func ServeSSE(w http.ResponseWriter, r *http.Request, hub *Hub, id string, terminalEvents ...string) {
	serveSSE(w, r, hub, id, nil, terminalEvents...)
}

func ServeSSEWithInitial(w http.ResponseWriter, r *http.Request, hub *Hub, id string, initial []HubEvent, terminalEvents ...string) {
	serveSSE(w, r, hub, id, initial, terminalEvents...)
}

func serveSSE(w http.ResponseWriter, r *http.Request, hub *Hub, id string, initial []HubEvent, terminalEvents ...string) {
	flusher, ok := w.(http.Flusher)
	if !ok {
		http.Error(w, "streaming not supported", http.StatusInternalServerError)
		return
	}

	w.Header().Set("Content-Type", "text/event-stream")
	w.Header().Set("Cache-Control", "no-cache")
	w.Header().Set("Connection", "keep-alive")
	w.Header().Set("X-Accel-Buffering", "no")
	w.WriteHeader(http.StatusOK)
	flusher.Flush()

	ch, unsubscribe := hub.Subscribe(id)
	defer unsubscribe()
	for _, event := range initial {
		fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, event.Data)
	}
	flusher.Flush()

	ticker := time.NewTicker(15 * time.Second)
	defer ticker.Stop()

	for {
		select {
		case <-r.Context().Done():
			return
		case <-ticker.C:
			fmt.Fprint(w, ": keepalive\n\n")
			flusher.Flush()
		case event, ok := <-ch:
			if !ok {
				return
			}
			fmt.Fprintf(w, "event: %s\ndata: %s\n\n", event.Type, event.Data)
			flusher.Flush()
			if isTerminalEvent(event.Type, terminalEvents) {
				return
			}
		}
	}
}

func isTerminalEvent(eventType string, terminalEvents []string) bool {
	if len(terminalEvents) == 0 {
		return eventType == "complete" || eventType == "error"
	}
	return slices.Contains(terminalEvents, eventType)
}

// mustJSON is a package-local alias for webproto.MustJSON.
var mustJSON = webproto.MustJSON
