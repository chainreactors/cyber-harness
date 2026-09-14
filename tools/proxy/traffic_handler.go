package proxy

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"sync/atomic"

	aop "github.com/chainreactors/aiscan/aop"
	traffic "github.com/chainreactors/aiscan/aop/traffic"
	"github.com/chainreactors/proxyclient"
	"github.com/chainreactors/proxyclient/extra/clash"
	protobuf "google.golang.org/protobuf/proto"
)

// trafficHandler bridges the AOP traffic namespace to the runner's traffic
// infrastructure: it applies Configure (routing + capture) against State/Hub,
// and answers Query with a State snapshot or recorded flows. Live completed
// flows are published only by the observe extension as typed AOP events.
type trafficHandler struct {
	hub *ProxyHub
}

// RegisterTrafficNamespace installs the proxy control surface directly on a
// connection-owned mux. The mux is the sole owner of admission and draining;
// the profile-owned hub has no lifecycle API to transfer.
func RegisterTrafficNamespace(mux *aop.NamespaceMux, hub *ProxyHub) error {
	if mux == nil || hub == nil || hub.store == nil || hub.state == nil {
		return fmt.Errorf("traffic namespace requires a mux and proxy hub")
	}
	h := &trafficHandler{hub: hub}
	return mux.Register("traffic", &traffic.ProtocolMessage{}, func(ctx context.Context, env *aop.Envelope, msg protobuf.Message, send aop.SendFunc) error {
		pm, ok := msg.(*traffic.ProtocolMessage)
		if !ok {
			return fmt.Errorf("traffic: unexpected message %T", msg)
		}
		return h.handle(ctx, env, pm, send)
	})
}

func (h *trafficHandler) handle(ctx context.Context, env *aop.Envelope, pm *traffic.ProtocolMessage, send aop.SendFunc) error {
	switch m := pm.Message.(type) {
	case *traffic.ProtocolMessage_Configure:
		return h.handleConfigure(ctx, env, m.Configure, send)
	case *traffic.ProtocolMessage_Query:
		return h.handleQuery(env, m.Query, send)
	default:
		// State and Flow are outbound-only; ignore if echoed back.
		return nil
	}
}

func (h *trafficHandler) handleConfigure(ctx context.Context, env *aop.Envelope, cfg *traffic.Configure, send aop.SendFunc) error {
	var errMsg string
	if rc := cfg.GetRouting(); rc != nil {
		if err := applyRouting(h.hub.state, rc); err != nil {
			errMsg = err.Error()
		}
	}
	if cap := cfg.GetCapture(); cap != nil && cap.GetMode() != traffic.CaptureMode_CAPTURE_MODE_UNSPECIFIED {
		record := cap.GetMode() == traffic.CaptureMode_CAPTURE_MODE_RECORD
		h.hub.SetCapture(record, cap.GetDecryptHttps())
		h.hub.SetCaptureFilter(cap.GetFilter())
	}
	return h.replyState(env.Id, send, errMsg)
}

func (h *trafficHandler) handleQuery(env *aop.Envelope, q *traffic.Query, send aop.SendFunc) error {
	if q.GetFlows() {
		for _, f := range h.hub.store.Query(queryOptsFromFilter(q.GetFilter())) {
			flow := f
			if err := h.sendFlow(env.Id, send, flow); err != nil {
				return err
			}
		}
	}
	// Always answer with a State unless the caller asked only for flows.
	if q.GetState() || !q.GetFlows() {
		return h.replyState(env.Id, send, "")
	}
	return nil
}

func (h *trafficHandler) sendFlow(replyTo string, send aop.SendFunc, flow Flow) error {
	env, err := aop.Wrap(trafficEnvID(), replyTo, &traffic.ProtocolMessage{
		Message: &traffic.ProtocolMessage_FlowRecord{FlowRecord: &traffic.FlowRecord{
			Operation: flow.Operation,
			Flow:      h.hub.store.flowToProto(&flow),
		}},
	})
	if err != nil {
		return err
	}
	return send(env)
}

func (h *trafficHandler) replyState(replyTo string, send aop.SendFunc, errMsg string) error {
	env, err := aop.Wrap(trafficEnvID(), replyTo, &traffic.ProtocolMessage{
		Message: &traffic.ProtocolMessage_State{State: h.snapshot(errMsg)},
	})
	if err != nil {
		return err
	}
	return send(env)
}

func (h *trafficHandler) snapshot(errMsg string) *traffic.State {
	if err := h.hub.store.IndexError(); err != nil {
		if errMsg != "" {
			errMsg += "; "
		}
		errMsg += "traffic index unavailable: " + err.Error()
	}
	s := h.hub.state
	mode := traffic.CaptureMode_CAPTURE_MODE_RELAY
	if h.hub.Capturing() {
		mode = traffic.CaptureMode_CAPTURE_MODE_RECORD
	}
	return &traffic.State{
		Routing: &traffic.RoutingState{
			ActiveNode: s.ActiveNodeName(),
			EgressUrl:  s.ActiveProxy(),
			Auto:       s.IsAutoMode(),
		},
		Capture: &traffic.CaptureState{Mode: mode, Capturing: h.hub.Capturing()},
		Error:   errMsg,
	}
}

// applyRouting steers the egress chain per the routing config. UNSPECIFIED
// leaves routing untouched so a capture-only Configure does not disturb it.
func applyRouting(state *State, rc *traffic.RoutingConfig) error {
	switch rc.GetMode() {
	case traffic.RoutingMode_ROUTING_MODE_UNSPECIFIED:
		return nil
	case traffic.RoutingMode_ROUTING_MODE_DIRECT, traffic.RoutingMode_ROUTING_MODE_CLEAR:
		state.Clear()
		return nil
	case traffic.RoutingMode_ROUTING_MODE_PROXY:
		if rc.GetUrl() == "" {
			return fmt.Errorf("routing proxy requires url")
		}
		return state.SetProxyURL(rc.GetUrl())
	case traffic.RoutingMode_ROUTING_MODE_SUBSCRIBE:
		sub, err := clash.FetchSubscriptionWithUA(rc.GetUrl(), clashSubscriptionUA)
		if err != nil {
			return fmt.Errorf("fetch subscription: %w", err)
		}
		state.LoadSubscription(sub, rc.GetUrl())
		return nil
	case traffic.RoutingMode_ROUTING_MODE_AUTO:
		return applyAutoRouting(state, rc)
	case traffic.RoutingMode_ROUTING_MODE_SWITCH:
		return state.Switch(rc.GetSelector())
	default:
		return fmt.Errorf("unknown routing mode %v", rc.GetMode())
	}
}

// applyAutoRouting mirrors the `proxy auto` verb: fetch the subscription and
// install an adaptive load-balancing clash dial as the persistent egress.
func applyAutoRouting(state *State, rc *traffic.RoutingConfig) error {
	if rc.GetUrl() == "" {
		return fmt.Errorf("routing auto requires url")
	}
	sub, err := clash.FetchSubscriptionWithUA(rc.GetUrl(), clashSubscriptionUA)
	if err != nil {
		return fmt.Errorf("fetch subscription: %w", err)
	}
	state.LoadSubscription(sub, rc.GetUrl())

	q := url.Values{}
	q.Set("url", rc.GetUrl())
	q.Set("ua", clashSubscriptionUA)
	strategy := rc.GetStrategy()
	if strategy == "" {
		strategy = "adaptive"
	}
	q.Set("strategy", strategy)
	if rc.GetType() != "" {
		q.Set("type", rc.GetType())
	}
	if rc.GetName() != "" {
		q.Set("name", rc.GetName())
	}
	if rc.GetCountry() != "" {
		q.Set("country", rc.GetCountry())
	}
	clashURL := "clash://?" + q.Encode()
	u, err := url.Parse(clashURL)
	if err != nil {
		return fmt.Errorf("build clash url: %w", err)
	}
	dial, err := proxyclient.NewClient(u)
	if err != nil {
		return fmt.Errorf("create dialer: %w", err)
	}
	state.SetAutoDial(clashURL, dial)
	return nil
}

func queryOptsFromFilter(f *traffic.FlowFilter) QueryOpts {
	if f == nil {
		return QueryOpts{}
	}
	return QueryOpts{
		Host:   f.GetHost(),
		Status: f.GetStatus(),
		CType:  f.GetType(),
		Last:   int(f.GetLast()),
	}
}

const clashSubscriptionUA = "clash-verge/v2.0.0"

var trafficEnvSeq atomic.Uint64

// trafficEnvID returns a process-unique envelope id for outbound traffic
// replies. A monotonic counter avoids time/random sources (unavailable in some
// hosts) while staying unique within a process.
func trafficEnvID() string {
	return "traffic:" + strconv.FormatUint(trafficEnvSeq.Add(1), 36)
}
