package proxy

import (
	"context"
	"crypto/rand"
	"net/url"
	"path/filepath"
	"strings"
	"sync"

	operationpb "github.com/chainreactors/cyber/aop/operation"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/proxyclient"
	"google.golang.org/protobuf/proto"
)

// NewHub constructs inert proxy infrastructure. originalProxy is the hub's
// default upstream, so tool → hub → configured proxy remains stable after Load.
//
// capture selects the hub mode (see NewProxyHub): true records traffic (mitm
// on), false is a pure routing relay (mitm off). Routing works in both, so
// `proxy` keeps managing egress either way; only capture is gated.
func NewHub(workDir, originalProxy string, capture bool, registry *hooks.Registry, storage ...cfg.TrafficOptions) (*Resource, error) {
	var config cfg.TrafficOptions
	if len(storage) > 0 {
		config = storage[0]
	}
	config, err := config.Normalize()
	if err != nil {
		return nil, err
	}
	state := NewState(originalProxy)

	// A clash:// original proxy is a subscription/auto spec, not a single node:
	// activate it as the auto dial so the hub's default upstream load-balances.
	if strings.HasPrefix(strings.ToUpper(originalProxy), "CLASH://") {
		if u, err := url.Parse(originalProxy); err == nil {
			if dial, dialErr := proxyclient.NewClient(u); dialErr == nil {
				state.SetAutoDial(originalProxy, dial)
			}
		}
	}

	store := NewFlowStoreWithLimits(10000, config.BodyRetentionBytes)
	caRoot := filepath.Join(workDir, ".cyber", "mitm")
	hub := NewProxyHub(state, store, caRoot, capture, registry)
	hub.ProxyHub.storage = config

	return hub, nil
}

type correlationLease struct {
	operation  *operationpb.Ref
	invocation operation.Invocation
	cancel     func(error) bool
	mu         sync.Mutex
	active     int
	released   bool
	done       chan struct{}
	doneOnce   sync.Once
}

type resolvedCorrelation struct {
	operation  *operationpb.Ref
	invocation operation.Invocation
	cancel     func(error) bool
	finish     func()
}

// Egress returns an opaque correlation lease for one real execution. The token
// is transport-only and cannot leak call/session identity through proxy auth.
// release must run when the actual HTTP owner or process exits.
func (h *ProxyHub) Egress(ctx context.Context) (string, string, func()) {
	if h == nil {
		return "", "", func() {}
	}
	base := h.ProxyURL()
	if base == "" {
		return "", h.CAPath(), func() {}
	}
	token := rand.Text()
	lease := &correlationLease{
		operation: operation.Correlation(ctx), invocation: operation.InvocationFromContext(ctx),
		cancel: func(cause error) bool { return operation.RequestCancel(ctx, cause) },
		done:   make(chan struct{}),
	}
	h.correlationMu.Lock()
	if h.correlations == nil {
		h.correlations = make(map[string]*correlationLease)
	}
	h.correlations[token] = lease
	h.correlationMu.Unlock()
	var once sync.Once
	release := func() {
		once.Do(func() {
			h.correlationMu.Lock()
			delete(h.correlations, token)
			h.correlationMu.Unlock()
			lease.mu.Lock()
			lease.released = true
			if lease.active == 0 {
				lease.doneOnce.Do(func() { close(lease.done) })
			}
			done := lease.done
			lease.mu.Unlock()
			<-done
		})
	}
	return egressURL(base, token), h.CAPath(), release
}

func (h *ProxyHub) resolveCorrelation(token string) resolvedCorrelation {
	if h != nil && token != "" {
		h.correlationMu.RLock()
		lease, ok := h.correlations[token]
		if ok && lease != nil {
			lease.mu.Lock()
			if !lease.released {
				lease.active++
				correlation := proto.Clone(lease.operation).(*operationpb.Ref)
				invocation, cancel := lease.invocation, lease.cancel
				lease.mu.Unlock()
				h.correlationMu.RUnlock()
				var once sync.Once
				return resolvedCorrelation{operation: correlation, invocation: invocation, cancel: cancel, finish: func() {
					once.Do(func() {
						lease.mu.Lock()
						lease.active--
						if lease.released && lease.active == 0 {
							lease.doneOnce.Do(func() { close(lease.done) })
						}
						lease.mu.Unlock()
					})
				}}
			}
			lease.mu.Unlock()
		}
		h.correlationMu.RUnlock()
	}
	return resolvedCorrelation{operation: &operationpb.Ref{Correlation: operationpb.Correlation_CORRELATION_UNATTRIBUTED}}
}

func egressURL(base, token string) string {
	if base == "" || token == "" {
		return base
	}
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	u.User = url.User(token)
	return u.String()
}
