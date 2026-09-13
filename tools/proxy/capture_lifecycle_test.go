package proxy

import (
	"context"
	"errors"
	"io"
	"strings"
	"testing"
	"time"

	traffic "github.com/chainreactors/aiscan/aop/traffic"
	mitmproxy "github.com/chainreactors/utils/mitmproxy/proxy"
)

func TestResponseEOFKeepsCaptureOpenUntilEngineCompletion(t *testing.T) {
	store := NewFlowStore(8)
	if err := store.SetBodyDir(t.TempDir()); err != nil {
		t.Fatal(err)
	}
	resource := NewProxyHub(NewState(""), store, "", true, nil)
	defer resource.Close(context.Background())
	hub := resource.ProxyHub
	addon := &captureAddon{hub: hub}
	flow := &mitmproxy.Flow{StartTime: time.Now()}
	state := newCaptureState(hub, flow)
	state.flow.Response = &traffic.Response{StatusCode: 200}
	addon.pending.Store(flow.Id.String(), state)
	reader := addon.StreamResponseModifier(flow, strings.NewReader("local response"))
	if _, err := io.Copy(io.Discard, reader); err != nil {
		t.Fatal(err)
	}
	if store.Count() != 0 {
		t.Fatal("response EOF published before forwarding completed")
	}
	if len(hub.bodySlots) != 1 {
		t.Fatal("recording slot was released before completion")
	}
	// The engine may learn about a downstream write failure after reading EOF.
	flow.Error = errors.New("downstream write failed")
	addon.FlowFinished(flow)
	addon.FlowFinished(flow)
	flows := store.Query(QueryOpts{})
	if len(flows) != 1 || flows[0].Complete || !strings.Contains(flows[0].Error, flow.Error.Error()) {
		t.Fatalf("completed flows = %+v", flows)
	}
	if string(flows[0].Response.Body) != "local response" {
		t.Fatal("completed capture lost retained bytes")
	}
	if len(hub.bodySlots) != 0 {
		t.Fatal("completed capture retained recording slot")
	}
}
