package proxy

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"testing"
	"time"

	operationpb "github.com/chainreactors/aiscan/aop/operation"
	traffic "github.com/chainreactors/aiscan/aop/traffic"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/operation"
)

// hubClient builds an HTTP client through an opaque operation-correlation lease.
func hubClient(t *testing.T, hub *ProxyHub, callID string) *http.Client {
	t.Helper()
	ctx := operation.ContextWithInvocation(t.Context(), operation.Invocation{CallID: callID})
	ctx, cancel := operation.Begin(ctx, "test", "http")
	proxyURL, _, release := hub.Egress(ctx)
	t.Cleanup(func() { release(); cancel(nil) })
	u, err := url.Parse(proxyURL)
	if err != nil {
		t.Fatalf("parse hub url: %v", err)
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
	resource := NewProxyHub(NewState(""), NewFlowStore(1000), caRoot, capture, nil)
	hub := resource.ProxyHub
	hub.storage = cfg.TrafficOptions{BodyStorage: "disk"}
	if err := resource.Start(t.Context()); err != nil {
		t.Fatalf("start hub: %v", err)
	}
	t.Cleanup(func() { resource.Close(context.Background()) })
	return hub
}

// TestHubStampsToolID verifies the hub attributes a captured flow to the tool-
// call id carried as the proxy username (via the mitmproxy fork's ProxyAuthUser).
func TestHubStampsToolID(t *testing.T) {
	target := startTestTarget(64)
	defer target.Close()
	hub := startHub(t, true)

	getThrough(t, hubClient(t, hub, "tool-abc"), target.URL)

	flows := waitForFlows(t, hub.store, 1)
	if len(flows) == 0 {
		t.Fatal("no flow captured")
	}
	if flows[0].Ref.GetCallId() != "tool-abc" {
		t.Fatalf("call id = %q, want %q", flows[0].Ref.GetCallId(), "tool-abc")
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
	if n := hub.store.Count(); n != 0 {
		t.Fatalf("relay mode recorded %d flows, want 0", n)
	}

	hub.SetCapture(true, true)
	if hub.ProxyURL() != addr {
		t.Fatalf("hub address changed on capture toggle: %q != %q", hub.ProxyURL(), addr)
	}
	getThrough(t, hubClient(t, hub, "tool-2"), target.URL)
	if flows := waitForFlows(t, hub.store, 1); len(flows) == 0 {
		t.Fatal("no flow captured after enabling capture")
	}
}

func TestFlowStoreReloadsMetadataIndexWithoutHydratingBodies(t *testing.T) {
	dir := t.TempDir()
	first := NewFlowStore(8)
	if err := first.SetBodyDir(dir); err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, "body", "capture-reload")
	if err := os.WriteFile(path, []byte("0123456789"), 0600); err != nil {
		t.Fatal(err)
	}
	addTestBody(t, first, Flow{
		Ref: &operationpb.Ref{CallId: "call-1"}, Host: "example.test", ContentType: "text/plain",
		Exchange: traffic.Exchange{
			Request:  traffic.Request{Method: "GET", URL: "https://example.test/"},
			Response: &traffic.Response{StatusCode: 200},
		},
	}, path)
	if err := first.Close(); err != nil {
		t.Fatal(err)
	}

	second := NewFlowStore(8)
	if err := second.SetBodyDir(dir); err != nil {
		t.Fatal(err)
	}
	defer second.Close()
	flows := second.Query(QueryOpts{})
	if len(flows) != 1 || flows[0].Ref.GetCallId() != "call-1" {
		t.Fatalf("reloaded flows = %#v", flows)
	}
	if flows[0].Response == nil || second.files[flows[0].ID][1] != 10 || len(flows[0].Response.Body) != 0 {
		t.Fatalf("reloaded body metadata = %#v", flows[0].Response)
	}
}
