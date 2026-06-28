package web

import (
	"encoding/json"
	"fmt"
	"net/http"
	"sync"
	"time"
)

// HubEvent is the unit broadcast through the SSE hub. Type is the SSE
// event name, Data is pre-serialized JSON written directly to the stream.
type HubEvent struct {
	Type string
	Data json.RawMessage
}

type BroadcastCallback func(id string, event HubEvent)

type Hub struct {
	mu          sync.Mutex
	subscribers map[string]map[chan HubEvent]struct{}
	callback    BroadcastCallback
}

func NewHub() *Hub {
	return &Hub{
		subscribers: make(map[string]map[chan HubEvent]struct{}),
	}
}

func (h *Hub) OnBroadcast(cb BroadcastCallback) {
	h.mu.Lock()
	h.callback = cb
	h.mu.Unlock()
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
	cb := h.callback
	for ch := range h.subscribers[id] {
		select {
		case ch <- event:
		default:
		}
	}
	h.mu.Unlock()
	if cb != nil {
		cb(id, event)
	}
}

func ServeSSE(w http.ResponseWriter, r *http.Request, hub *Hub, id string, terminalEvents ...string) {
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
	for _, t := range terminalEvents {
		if eventType == t {
			return true
		}
	}
	return false
}

// mustJSON marshals v to json.RawMessage. Panics on error (should never
// happen with map/struct inputs).
func mustJSON(v any) json.RawMessage {
	data, err := json.Marshal(v)
	if err != nil {
		panic(err)
	}
	return data
}
