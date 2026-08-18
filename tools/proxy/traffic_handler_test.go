package proxy

import (
	"context"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	traffic "github.com/chainreactors/aiscan/aop/traffic"
)

// collectReplies dispatches one envelope through a mux registered for the
// traffic handler and returns every reply the handler sends synchronously.
func dispatchTraffic(t *testing.T, h *TrafficHandler, msg *traffic.ProtocolMessage) []*traffic.ProtocolMessage {
	t.Helper()
	mux := aop.NewNamespaceMux()
	if err := h.Register(mux); err != nil {
		t.Fatalf("register: %v", err)
	}
	env := aop.MustWrap("req-1", "", msg)
	var replies []*traffic.ProtocolMessage
	_, err := mux.Dispatch(context.Background(), env, func(reply *aop.Envelope) error {
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

func TestTrafficHandlerConfigureCapture(t *testing.T) {
	hub := startHub(t, false) // relay
	infra := &Infra{State: hub.state, Store: hub.store, Hub: hub}
	h := NewTrafficHandler(infra)
	defer h.Close()

	if hub.Capturing() {
		t.Fatal("hub should start in relay mode")
	}

	replies := dispatchTraffic(t, h, &traffic.ProtocolMessage{
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
	dispatchTraffic(t, h, &traffic.ProtocolMessage{
		Message: &traffic.ProtocolMessage_Configure{Configure: &traffic.Configure{
			Capture: &traffic.CaptureConfig{Mode: traffic.CaptureMode_CAPTURE_MODE_RELAY},
		}},
	})
	if hub.Capturing() {
		t.Fatal("relay Configure did not disable capture")
	}
}

func TestTrafficHandlerConfigureRoutingProxy(t *testing.T) {
	hub := startHub(t, false)
	infra := &Infra{State: hub.state, Store: hub.store, Hub: hub}
	h := NewTrafficHandler(infra)
	defer h.Close()

	replies := dispatchTraffic(t, h, &traffic.ProtocolMessage{
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

func TestTrafficHandlerQueryFlows(t *testing.T) {
	target := startTestTarget(64)
	defer target.Close()
	hub := startHub(t, true) // record
	infra := &Infra{State: hub.state, Store: hub.store, Hub: hub}
	h := NewTrafficHandler(infra)
	defer h.Close()

	getThrough(t, hubClient(t, hub, "tool-q"), target.URL)
	if flows := waitForFlows(t, hub.Store(), 1); len(flows) == 0 {
		t.Fatal("no flow captured")
	}

	replies := dispatchTraffic(t, h, &traffic.ProtocolMessage{
		Message: &traffic.ProtocolMessage_Query{Query: &traffic.Query{Flows: true}},
	})

	var flowReplies int
	for _, r := range replies {
		if f := r.GetFlow(); f != nil {
			flowReplies++
			if f.GetToolId() != "tool-q" {
				t.Fatalf("queried flow tool_id = %q, want tool-q", f.GetToolId())
			}
		}
	}
	if flowReplies == 0 {
		t.Fatal("Query flows returned no Flow replies")
	}
}
