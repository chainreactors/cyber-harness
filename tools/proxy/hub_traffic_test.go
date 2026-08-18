package proxy

import (
	"context"
	"io"
	"net/http"
	"net/url"
	"testing"
	"time"
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
