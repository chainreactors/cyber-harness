package proxy

import (
	"context"
	"fmt"
	"net/url"
	"strconv"
	"sync"
	"sync/atomic"

	aop "github.com/chainreactors/aiscan/aop"
	traffic "github.com/chainreactors/aiscan/aop/traffic"
	"github.com/chainreactors/proxyclient"
	"github.com/chainreactors/proxyclient/extra/clash"
	protobuf "google.golang.org/protobuf/proto"
)

// TrafficHandler bridges the AOP traffic namespace to the runner's traffic
// infrastructure: it applies Configure (routing + capture) against State/Hub,
// answers Query with a State snapshot or recorded flows, and streams captured
// flows back while a stream is requested. One handler is created per connection
// so its stream lifecycle is tied to that connection.
type TrafficHandler struct {
	infra *Infra

	mu         sync.Mutex
	stopStream func() // cancels the active flow stream, nil when none
}

// NewTrafficHandler returns a handler backed by infra. infra must be non-nil and
// fully started (hub listening).
func NewTrafficHandler(infra *Infra) *TrafficHandler {
	return &TrafficHandler{infra: infra}
}

// Register installs the traffic namespace on mux. The returned mux routes
// traffic.ProtocolMessage envelopes to this handler.
func (h *TrafficHandler) Register(mux *aop.NamespaceMux) error {
	return mux.Register(&traffic.ProtocolMessage{}, func(ctx context.Context, env *aop.Envelope, msg protobuf.Message, send aop.SendFunc) error {
		pm, ok := msg.(*traffic.ProtocolMessage)
		if !ok {
			return fmt.Errorf("traffic: unexpected message %T", msg)
		}
		return h.handle(ctx, env, pm, send)
	})
}

// Close tears down any active stream. Call when the connection ends.
func (h *TrafficHandler) Close() { h.stopStreaming() }

func (h *TrafficHandler) handle(ctx context.Context, env *aop.Envelope, pm *traffic.ProtocolMessage, send aop.SendFunc) error {
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

func (h *TrafficHandler) handleConfigure(ctx context.Context, env *aop.Envelope, cfg *traffic.Configure, send aop.SendFunc) error {
	var errMsg string
	if rc := cfg.GetRouting(); rc != nil {
		if err := applyRouting(h.infra.State, rc); err != nil {
			errMsg = err.Error()
		}
	}
	if cap := cfg.GetCapture(); cap != nil && cap.GetMode() != traffic.CaptureMode_CAPTURE_MODE_UNSPECIFIED {
		record := cap.GetMode() == traffic.CaptureMode_CAPTURE_MODE_RECORD
		h.infra.Hub.SetCapture(record, cap.GetDecryptHttps())
		h.infra.Hub.SetCaptureFilter(cap.GetFilter())
		if record && cap.GetStream() {
			h.startStream(ctx, env.Id, send)
		} else {
			h.stopStreaming()
		}
	}
	return h.replyState(env.Id, send, errMsg)
}

func (h *TrafficHandler) handleQuery(env *aop.Envelope, q *traffic.Query, send aop.SendFunc) error {
	if q.GetFlows() {
		for _, f := range h.infra.Store.Query(queryOptsFromFilter(q.GetFilter())) {
			flow := f
			if err := h.sendFlow(env.Id, send, flowToProto(&flow)); err != nil {
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

// startStream subscribes to the hub and forwards captured flows as Flow messages
// correlated to replyTo until the connection context ends or capture is
// reconfigured. A prior stream is replaced.
func (h *TrafficHandler) startStream(ctx context.Context, replyTo string, send aop.SendFunc) {
	ch, cancelSub := h.infra.Hub.Subscribe(256)
	streamCtx, cancelCtx := context.WithCancel(ctx)

	h.mu.Lock()
	if h.stopStream != nil {
		h.stopStream()
	}
	h.stopStream = func() {
		cancelCtx()
		cancelSub()
	}
	h.mu.Unlock()

	go func() {
		for {
			select {
			case <-streamCtx.Done():
				return
			case flow, ok := <-ch:
				if !ok {
					return
				}
				if err := h.sendFlow(replyTo, send, flow); err != nil {
					return
				}
			}
		}
	}()
}

func (h *TrafficHandler) stopStreaming() {
	h.mu.Lock()
	defer h.mu.Unlock()
	if h.stopStream != nil {
		h.stopStream()
		h.stopStream = nil
	}
}

func (h *TrafficHandler) sendFlow(replyTo string, send aop.SendFunc, flow *traffic.Flow) error {
	env, err := aop.Wrap(trafficEnvID(), replyTo, &traffic.ProtocolMessage{
		Message: &traffic.ProtocolMessage_Flow{Flow: flow},
	})
	if err != nil {
		return err
	}
	return send(env)
}

func (h *TrafficHandler) replyState(replyTo string, send aop.SendFunc, errMsg string) error {
	env, err := aop.Wrap(trafficEnvID(), replyTo, &traffic.ProtocolMessage{
		Message: &traffic.ProtocolMessage_State{State: h.snapshot(errMsg)},
	})
	if err != nil {
		return err
	}
	return send(env)
}

func (h *TrafficHandler) snapshot(errMsg string) *traffic.State {
	s := h.infra.State
	mode := traffic.CaptureMode_CAPTURE_MODE_RELAY
	if h.infra.Hub.Capturing() {
		mode = traffic.CaptureMode_CAPTURE_MODE_RECORD
	}
	return &traffic.State{
		Routing: &traffic.RoutingState{
			ActiveNode: s.ActiveNodeName(),
			EgressUrl:  s.ActiveProxy(),
			Auto:       s.IsAutoMode(),
		},
		Capture: &traffic.CaptureState{Mode: mode, Capturing: h.infra.Hub.Capturing()},
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
