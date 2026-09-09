package proxy

import (
	"context"
	"fmt"
	"os"
	"strconv"

	traffic "github.com/chainreactors/aiscan/aop/traffic"
	"github.com/chainreactors/aiscan/core/eventbus"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (h *ProxyHub) Store() *FlowStore { return h.store }

func (h *ProxyHub) ingest(flow Flow) { h.ingestFiles(flow, [2]*os.File{}) }

func (h *ProxyHub) ingestFiles(flow Flow, files [2]*os.File) {
	if h.store == nil {
		return
	}
	h.subsMu.Lock()
	defer h.subsMu.Unlock()
	if h.closed || !h.recording.Load() || !h.captureMatches(flow) {
		removeCaptureFiles(files)
		return
	}
	h.store.addFiles(flow, files)
}

// Subscribe preserves the historical channel API. New consumers should use
// SubscribeFlows and deliver directly from the eventbus handler.
func (h *ProxyHub) Subscribe(buffer int) (<-chan *traffic.Flow, func()) {
	return h.SubscribeFrom(-1, buffer)
}

// SubscribeFrom queues only metadata; its output is unbuffered. Oversized
// replay terminates the subscription rather than silently skipping entries.
func (h *ProxyHub) SubscribeFrom(after int, buffer int) (<-chan *traffic.Flow, func()) {
	out := make(chan *traffic.Flow)
	ctx, stop := context.WithCancel(context.Background())
	if buffer <= 0 {
		buffer = 256
	}
	sub, err := h.SubscribeFlows(after, eventbus.SubscribeOptions[Flow]{Buffer: buffer}, func(flow Flow) error {
		select {
		case <-ctx.Done():
			return nil
		default:
		}
		message := h.store.flowToProto(&flow)
		select {
		case out <- message:
		case <-ctx.Done():
		}
		return nil
	})
	if err != nil {
		stop()
		close(out)
		return out, func() {}
	}
	go func() { <-sub.Stopped(); stop(); <-sub.Done(); close(out) }()
	return out, func() { stop(); sub.Cancel() }
}

// SubscribeFlows is a native eventbus subscription: filters run before
// admission; a serial handler receives owned metadata and may stream or write
// it. Body hydration is the consumer's choice. after < 0 starts at the tail.
// Filters must be pure, fast, and must not reenter the hub/store.
func (h *ProxyHub) SubscribeFlows(after int, opts eventbus.SubscribeOptions[Flow], handler func(Flow) error) (*eventbus.Subscription[Flow], error) {
	if opts.MaxBytes == 0 {
		opts.MaxBytes = 4 << 20
	}
	opts.Size = flowMetadataSize
	opts.Clone = cloneFlowMetadata
	// A private admission bus lets replay target this consumer only. Register
	// the live forwarding subscription under the same lock as Add and replay.
	bus := eventbus.New[Flow]()
	sub, err := bus.SubscribeAsync(opts, handler)
	if err != nil {
		return nil, err
	}
	h.subsMu.Lock()
	h.store.publishMu.Lock()
	unsub := h.store.events.Subscribe(bus.Emit)
	id := h.nextSub
	h.nextSub++
	if h.subs == nil {
		h.subs = make(map[int]func())
	}
	h.subs[id] = sub.Cancel
	if h.closed {
		sub.Cancel()
	} else if after >= 0 {
		for _, flow := range h.store.after(after) {
			bus.Emit(flow)
		}
	}
	h.store.publishMu.Unlock()
	h.subsMu.Unlock()
	go func() {
		<-sub.Stopped()
		unsub()
		h.subsMu.Lock()
		delete(h.subs, id)
		h.subsMu.Unlock()
	}()
	return sub, nil
}

func cloneFlowMetadata(flow Flow) Flow {
	flow.Exchange = flow.Clone()
	return flow
}

// Includes previews and variable-sized headers/strings, plus conservative
// fixed overhead. There is also an independent event count limit.
func flowMetadataSize(flow Flow) int64 {
	size := int64(1024 + len(flow.ID) + len(flow.ToolID) + len(flow.Host) + len(flow.ContentType) + len(flow.Error))
	size += int64(len(flow.Request.URL) + len(flow.Request.Method) + len(flow.Request.Protocol) + len(flow.Request.Body))
	for _, pair := range flow.Request.Headers {
		size += int64(len(pair.Name) + len(pair.Value) + 64)
	}
	if flow.Response != nil {
		size += int64(len(flow.Response.Body) + len(flow.Response.ReasonPhrase))
		for _, pair := range flow.Response.Headers {
			size += int64(len(pair.Name) + len(pair.Value) + 64)
		}
	}

	return size
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
	return renderFlowToProto(nil, flow)
}

// flowToProto hydrates through the store's body read lock when rendering an
// internal stream/query response. The lock keeps ring eviction from deleting a
// file between the copy and the read. The package-level helper above remains
// for callers/tests that do not have a store handle.
func (s *FlowStore) flowToProto(flow *Flow) *traffic.Flow {
	return renderFlowToProto(s, flow)
}

func renderFlowToProto(store *FlowStore, flow *Flow) *traffic.Flow {
	if flow == nil {
		return nil
	}
	// The hot Flow contains only previews. File ownership stays in the store;
	// load bytes only at this boundary, never into a subscriber queue.
	copy := *flow
	copy.Exchange = flow.Clone()
	if store != nil {
		if err := store.hydrate(&copy); err != nil {
			copy.Complete = false
			copy.Error += fmt.Sprintf("; body unavailable: %v", err)
		}

	}
	message := copy.Proto()
	message.ToolId = flow.ToolID
	if !flow.Timestamp.IsZero() {
		message.Timestamp = timestamppb.New(flow.Timestamp)
	}
	return message
}
