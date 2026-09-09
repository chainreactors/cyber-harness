package proxy

import (
	"context"
	"errors"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"testing"
	"time"

	traffic "github.com/chainreactors/aiscan/aop/traffic"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
)

// hubClient builds an HTTP client that routes through the hub with callID as the
// proxy username, mirroring how bash injects the tool-call id as proxy userinfo.
func hubClient(t *testing.T, hub *ProxyHub, callID string) *http.Client {
	t.Helper()
	u, err := url.Parse(hub.ProxyURL())
	if err != nil {
		t.Fatalf("parse hub url: %v", err)
	}
	if callID != "" {
		u.User = url.User(callID)
	}
	return &http.Client{
		Transport: &http.Transport{Proxy: http.ProxyURL(u), DisableKeepAlives: true},
		Timeout:   5 * time.Second,
	}
}

func getThrough(t *testing.T, client *http.Client, target string) {
	t.Helper()
	resp, err := client.Get(target)
	if err != nil {
		t.Fatalf("request through hub: %v", err)
	}
	io.Copy(io.Discard, resp.Body)
	resp.Body.Close()
}

func waitForFlows(t *testing.T, store *FlowStore, want int) []Flow {
	t.Helper()
	deadline := time.Now().Add(2 * time.Second)
	for time.Now().Before(deadline) {
		if flows := store.Query(QueryOpts{}); len(flows) >= want {
			return flows
		}
		time.Sleep(10 * time.Millisecond)
	}
	return store.Query(QueryOpts{})
}

func startHub(t *testing.T, capture bool) *ProxyHub {
	t.Helper()
	caRoot := t.TempDir()
	hub := NewProxyHub(NewState(""), NewFlowStore(1000), caRoot, capture)
	hub.storage = cfg.TrafficOptions{BodyStorage: "disk"}
	if err := hub.Start(caRoot); err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(func() { hub.Shutdown(context.Background()) })
	return hub
}

// TestHubStampsToolID verifies the hub attributes a captured flow to the tool-
// call id carried as the proxy username (via the mitmproxy fork's ProxyAuthUser).
func TestHubStampsToolID(t *testing.T) {
	target := startTestTarget(64)
	defer target.Close()
	hub := startHub(t, true)

	getThrough(t, hubClient(t, hub, "tool-abc"), target.URL)

	flows := waitForFlows(t, hub.Store(), 1)
	if len(flows) == 0 {
		t.Fatal("no flow captured")
	}
	if flows[0].ToolID != "tool-abc" {
		t.Fatalf("ToolID = %q, want %q", flows[0].ToolID, "tool-abc")
	}
}

// TestHubCaptureToggle verifies capture is runtime-mutable: a relay-mode hub
// records nothing until SetCapture turns recording on, without restarting.
func TestHubCaptureToggle(t *testing.T) {
	target := startTestTarget(64)
	defer target.Close()
	hub := startHub(t, false) // relay
	addr := hub.ProxyURL()

	getThrough(t, hubClient(t, hub, "tool-1"), target.URL)
	time.Sleep(100 * time.Millisecond)
	if n := hub.Store().Count(); n != 0 {
		t.Fatalf("relay mode recorded %d flows, want 0", n)
	}

	hub.SetCapture(true, true)
	if hub.ProxyURL() != addr {
		t.Fatalf("hub address changed on capture toggle: %q != %q", hub.ProxyURL(), addr)
	}
	getThrough(t, hubClient(t, hub, "tool-2"), target.URL)
	if flows := waitForFlows(t, hub.Store(), 1); len(flows) == 0 {
		t.Fatal("no flow captured after enabling capture")
	}
}

// TestHubSubscribe verifies captured flows fan out to subscribers as protocol
// messages carrying the tool-call id.
func TestHubSubscribe(t *testing.T) {
	target := startTestTarget(64)
	defer target.Close()
	hub := startHub(t, true)

	ch, cancel := hub.Subscribe(16)
	defer cancel()

	getThrough(t, hubClient(t, hub, "tool-xyz"), target.URL)

	select {
	case flow := <-ch:
		if flow.GetToolId() != "tool-xyz" {
			t.Fatalf("streamed ToolId = %q, want %q", flow.GetToolId(), "tool-xyz")
		}
	case <-time.After(2 * time.Second):
		t.Fatal("no flow received on subscription")
	}
}

func TestHubSlowSubscriberStopsWithoutAffectingHealthySubscriber(t *testing.T) {
	hub := NewProxyHub(NewState(""), NewFlowStore(128), "", true)
	gate := make(chan struct{})
	slow, err := hub.SubscribeFlows(-1, eventbus.SubscribeOptions[Flow]{Buffer: 1}, func(Flow) error { <-gate; return nil })
	if err != nil {
		t.Fatal(err)
	}
	defer slow.Cancel()
	healthyFlows := make(chan *traffic.Flow, 128)
	healthy, err := hub.SubscribeFlows(-1, eventbus.SubscribeOptions[Flow]{Buffer: 128}, func(f Flow) error {
		healthyFlows <- flowToProto(&f)
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	defer healthy.Cancel()

	// Both queues are bounded. A consumer that cannot keep up is explicitly
	// terminated rather than silently skipping observations.
	for i := 1; i <= 64; i++ {
		hub.ingest(Flow{Exchange: traffic.Exchange{
			Request:  traffic.Request{Method: "GET", URL: "https://example.test/"},
			Response: &traffic.Response{StatusCode: 200},
		}, ToolID: "tool"})
	}
	close(gate)
	select {
	case <-slow.Done():
	case <-time.After(2 * time.Second):
		t.Fatal("slow subscriber did not stop")
	}
	if !errors.Is(slow.Err(), eventbus.ErrOverflow) {
		t.Fatalf("slow error = %v", slow.Err())
	}

	for i := 1; i <= 64; i++ {
		select {
		case got := <-healthyFlows:
			if got == nil || got.GetId() != strconv.Itoa(i) {
				t.Fatalf("flow %d = %#v, want sequential id %d", i, got, i)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for flow %d", i)
		}
	}
}

func TestHubSubscribeFromReplaysRetainedFlows(t *testing.T) {
	hub := NewProxyHub(NewState(""), NewFlowStore(8), "", true)
	for i := 1; i <= 3; i++ {
		hub.ingest(Flow{Exchange: traffic.Exchange{
			Request:  traffic.Request{Method: "GET", URL: "https://example.test/"},
			Response: &traffic.Response{StatusCode: 200},
		}})
	}
	ch, cancel := hub.SubscribeFrom(1, 2)
	defer cancel()
	for want := 2; want <= 3; want++ {
		select {
		case got := <-ch:
			if got == nil || got.GetId() != strconv.Itoa(want) {
				t.Fatalf("replayed flow = %#v, want id %d", got, want)
			}
		case <-time.After(2 * time.Second):
			t.Fatalf("timed out waiting for replayed flow %d", want)
		}
	}
}

func TestFlowStoreReloadsMetadataIndexWithoutHydratingBodies(t *testing.T) {
	dir := t.TempDir()
	first := NewFlowStore(8)
	if err := first.SetBodyDir(dir); err != nil {
		t.Fatal(err)
	}
	first.Add(Flow{
		ToolID: "call-1", Host: "example.test", ContentType: "text/plain",
		Exchange: traffic.Exchange{
			Request:  traffic.Request{Method: "GET", URL: "https://example.test/"},
			Response: &traffic.Response{StatusCode: 200, BodyRef: &traffic.BodyRef{Path: "body/1.resp", Size: 10, Complete: true}},
		},
	})
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second := NewFlowStore(8)
	if err := second.SetBodyDir(dir); err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	flows := second.Query(QueryOpts{})
	if len(flows) != 1 || flows[0].ToolID != "call-1" {
		t.Fatalf("reloaded flows = %#v", flows)
	}
	if flows[0].Response == nil || flows[0].Response.BodyRef == nil || len(flows[0].Response.Body) != 0 {
		t.Fatalf("reloaded body metadata = %#v", flows[0].Response)
	}
}
