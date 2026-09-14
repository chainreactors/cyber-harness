package proxy

import (
	"context"
	"fmt"
	"os"
	"strconv"

	operationpb "github.com/chainreactors/aiscan/aop/operation"
	traffic "github.com/chainreactors/aiscan/aop/traffic"
	corehooks "github.com/chainreactors/aiscan/core/hooks"
	"github.com/chainreactors/aiscan/core/operation"
	toolhooks "github.com/chainreactors/aiscan/core/tool/hooks"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (h *ProxyHub) ingest(flow Flow) { h.ingestFiles(flow, [2]*os.File{}) }

func (h *ProxyHub) ingestFiles(flow Flow, files [2]*os.File) {
	if h.store == nil {
		return
	}
	if h.stopping.Load() || !h.recording.Load() || !h.captureMatches(flow) {
		removeCaptureFiles(files)
		return
	}
	correlation := flow
	if flow.Operation == nil {
		flow.Operation = &operationpb.Ref{Correlation: operationpb.Correlation_CORRELATION_UNATTRIBUTED}
	}
	stored := h.store.addFiles(flow, files)
	message := h.store.flowToProto(&stored)
	ctx := operation.ContextWithInvocation(context.Background(), correlation.Invocation)
	makeEvent := func() toolhooks.FlowEvent {
		return toolhooks.FlowEvent{
			Operation: proto.Clone(stored.Operation).(*operationpb.Ref),
			Flow:      proto.Clone(message).(*traffic.Flow),
		}
	}
	if h.hooks.Has(toolhooks.FlowCompletedControl.Kind) {
		response, hookErr := toolhooks.FlowCompletedControl.Emit(ctx, h.hooks, makeEvent())
		if cause := toolhooks.CancellationCause(response, hookErr); cause != nil && correlation.cancel != nil {
			correlation.cancel(cause)
		}
	}
	if h.hooks.Has(toolhooks.FlowCompletedObserved.Kind) {
		corehooks.Notify(ctx, h.hooks, toolhooks.FlowCompletedObserved, makeEvent())
	}
}

func cloneFlowMetadata(flow Flow) Flow {
	flow.Exchange = flow.Clone()
	if flow.Operation != nil {
		flow.Operation = proto.Clone(flow.Operation).(*operationpb.Ref)
	}
	flow.Invocation.Progress = nil
	flow.cancel = nil
	return flow
}

// Includes previews and variable-sized headers/strings, plus conservative
// fixed overhead. There is also an independent event count limit.
func flowMetadataSize(flow Flow) int64 {
	size := int64(1024 + len(flow.ID) + proto.Size(flow.Operation) + len(flow.Host) + len(flow.ContentType) + len(flow.Error))
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

// flowToProto hydrates through the store's body read lock when rendering a
// query response. The lock keeps ring eviction from deleting a
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
	if !flow.Timestamp.IsZero() {
		message.Timestamp = timestamppb.New(flow.Timestamp)
	}
	return message
}
