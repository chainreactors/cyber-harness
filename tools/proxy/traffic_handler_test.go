package proxy

import (
	"context"
	"errors"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	traffic "github.com/chainreactors/aiscan/aop/traffic"
)

// dispatchTraffic installs one connection-owned namespace and returns every
// synchronous reply to msg.
func dispatchTraffic(t *testing.T, hub *ProxyHub, msg *traffic.ProtocolMessage) []*traffic.ProtocolMessage {
	t.Helper()
	mux := aop.NewNamespaceMux(t.Context())
	defer mux.Close(context.Background())
	if err := RegisterTrafficNamespace(mux, hub); err != nil {
		t.Fatalf("register: %v", err)
	}
	env := aop.MustWrap("req-1", "", msg)
	var replies []*traffic.ProtocolMessage
	_, err := mux.Dispatch(env, func(reply *aop.Envelope) error {
		m, err := aop.Unwrap(reply)
		if err != nil {
			return err
		}
		if pm, ok := m.(*traffic.ProtocolMessage); ok {
			replies = append(replies, pm)
		}
		return nil
	})
	if err != nil {
		t.Fatalf("dispatch: %v", err)
	}
	return replies
}

func TestTrafficNamespaceRegistration(t *testing.T) {
	hub := startHub(t, false)
	mux := aop.NewNamespaceMux(t.Context())
	request := aop.MustWrap("query", "", &traffic.ProtocolMessage{
		Message: &traffic.ProtocolMessage_Query{Query: &traffic.Query{State: true}},
	})
	if handled, err := mux.Dispatch(request, nil); handled || err != nil {
		t.Fatalf("constructor published namespace: %v %v", handled, err)
	}
	if err := RegisterTrafficNamespace(mux, hub); err != nil {
		t.Fatal(err)
	}
	if err := RegisterTrafficNamespace(mux, hub); err == nil {
		t.Fatal("duplicate namespace installation succeeded")
	}
	replies := 0
	if handled, err := mux.Dispatch(request, func(*aop.Envelope) error { replies++; return nil }); !handled || err != nil || replies != 1 {
		t.Fatalf("duplicate registration affected owner: %v %v replies=%d", handled, err, replies)
	}
	if err := mux.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if _, err := mux.Dispatch(request, nil); !errors.Is(err, aop.ErrNamespaceUnavailable) {
		t.Fatalf("closed handler still admitted queries: %v", err)
	}
	// Closing the protocol handler must leave the profile-owned proxy usable.
	target := startTestTarget(16)
	defer target.Close()
	getThrough(t, hubClient(t, hub, "still-running"), target.URL)
}

func TestTrafficNamespaceConfigureCapture(t *testing.T) {
	hub := startHub(t, false) // relay
	infra := hub
	if hub.Capturing() {
		t.Fatal("hub should start in relay mode")
	}

	replies := dispatchTraffic(t, infra, &traffic.ProtocolMessage{
		Message: &traffic.ProtocolMessage_Configure{Configure: &traffic.Configure{
			Capture: &traffic.CaptureConfig{Mode: traffic.CaptureMode_CAPTURE_MODE_RECORD, DecryptHttps: true},
		}},
	})

	if !hub.Capturing() {
		t.Fatal("Configure did not enable capture")
	}
	if len(replies) != 1 {
		t.Fatalf("want 1 State reply, got %d", len(replies))
	}
	state := replies[0].GetState()
	if state == nil {
		t.Fatalf("reply is not a State: %#v", replies[0])
	}
	if state.GetCapture().GetMode() != traffic.CaptureMode_CAPTURE_MODE_RECORD || !state.GetCapture().GetCapturing() {
		t.Fatalf("State capture = %#v, want RECORD/capturing", state.GetCapture())
	}

	// A relay Configure turns capture back off.
	dispatchTraffic(t, infra, &traffic.ProtocolMessage{
		Message: &traffic.ProtocolMessage_Configure{Configure: &traffic.Configure{
			Capture: &traffic.CaptureConfig{Mode: traffic.CaptureMode_CAPTURE_MODE_RELAY},
		}},
	})
	if hub.Capturing() {
		t.Fatal("relay Configure did not disable capture")
	}
}

func TestTrafficNamespaceConfigureRoutingProxy(t *testing.T) {
	hub := startHub(t, false)
	infra := hub
	replies := dispatchTraffic(t, infra, &traffic.ProtocolMessage{
		Message: &traffic.ProtocolMessage_Configure{Configure: &traffic.Configure{
			Routing: &traffic.RoutingConfig{Mode: traffic.RoutingMode_ROUTING_MODE_PROXY, Url: "socks5://127.0.0.1:1080"},
		}},
	})
	if len(replies) != 1 || replies[0].GetState() == nil {
		t.Fatalf("want 1 State reply, got %#v", replies)
	}
	if got := replies[0].GetState().GetRouting().GetEgressUrl(); got != "socks5://127.0.0.1:1080" {
		t.Fatalf("egress url = %q, want socks5://127.0.0.1:1080", got)
	}
}

func TestTrafficNamespaceQueryFlows(t *testing.T) {
	target := startTestTarget(64)
	defer target.Close()
	hub := startHub(t, true) // record
	infra := hub
	getThrough(t, hubClient(t, hub, "tool-q"), target.URL)
	if flows := waitForFlows(t, hub.store, 1); len(flows) == 0 {
		t.Fatal("no flow captured")
	}

	replies := dispatchTraffic(t, infra, &traffic.ProtocolMessage{
		Message: &traffic.ProtocolMessage_Query{Query: &traffic.Query{Flows: true}},
	})

	var flowReplies int
	for _, r := range replies {
		if record := r.GetFlowRecord(); record != nil {
			flowReplies++
			if record.GetFlow().GetId() == "" || record.GetOperation().GetCallId() != "tool-q" {
				t.Fatalf("queried flow record = %v", record)
			}
		}
	}
	if flowReplies == 0 {
		t.Fatal("Query flows returned no Flow replies")
	}
}
