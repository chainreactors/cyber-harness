package proxy

import (
	"context"
	"fmt"
	"os"
	"strconv"

	operationpb "github.com/chainreactors/cyber/aop/operation"
	traffic "github.com/chainreactors/cyber/aop/traffic"
	corehooks "github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
	"google.golang.org/protobuf/proto"
)

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
	if toolhooks.FlowCompletedControl.Has(h.hooks) {
		response, hookErr := toolhooks.FlowCompletedControl.Emit(ctx, h.hooks, makeEvent())
		if cause := toolhooks.CancellationCause(response, hookErr); cause != nil && correlation.cancel != nil {
			correlation.cancel(cause)
		}
	}
	if toolhooks.FlowCompletedObserved.Has(h.hooks) {
		corehooks.Notify(ctx, h.hooks, toolhooks.FlowCompletedObserved, makeEvent())
	}
}

func cloneFlowMetadata(flow Flow) Flow {
	// The canonical flow is held by pointer, so the zero Flow is nil; normalize
	// here to keep the zero value valid for callers and for proto.Clone.
	if flow.Flow == nil {
		flow.Flow = &traffic.Flow{}
	}
	flow.Flow = proto.Clone(flow.Flow).(*traffic.Flow)
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
	size := int64(1024 + len(flow.Id) + proto.Size(flow.Operation) + len(flow.Host) + len(flow.ContentType) + len(flow.Error))
	if flow.Request != nil {
		size += int64(len(flow.Request.Url) + len(flow.Request.Method) + len(flow.Request.Protocol) + len(flow.Request.Body))
		for _, pair := range flow.Request.Headers {
			if pair != nil {
				size += int64(len(pair.Name) + len(pair.Value) + 64)
			}
		}
	}
	if flow.Response != nil {
		size += int64(len(flow.Response.Body) + len(flow.Response.ReasonPhrase))
		for _, pair := range flow.Response.Headers {
			if pair != nil {
				size += int64(len(pair.Name) + len(pair.Value) + 64)
			}
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

// flowToProto renders a stored canonical Flow. Hydration goes through the
// store's body read lock, which keeps ring
// eviction from deleting a file between the copy and the read.
func (s *FlowStore) flowToProto(flow *Flow) *traffic.Flow {
	if flow == nil {
		return nil
	}
	// The hot Flow contains only previews. File ownership stays in the store;
	// load bytes only at this boundary, never into a subscriber queue.
	copy := cloneFlowMetadata(*flow)
	if s != nil {
		if err := s.hydrate(&copy); err != nil {
			copy.Complete = false
			copy.Error += fmt.Sprintf("; body unavailable: %v", err)
		}
	}
	return copy.Flow
}
