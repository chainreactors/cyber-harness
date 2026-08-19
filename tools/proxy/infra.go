package proxy

import (
	"net/url"
	"path/filepath"
	"strings"

	"github.com/chainreactors/aiscan/core/deps"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/proxyclient"
)

// InfraKey carries the long-lived proxy infrastructure from the assembly layer
// into the proxy command factory through the Deps bag.
var InfraKey = deps.NewKey[*Infra]("proxy.infra")

// Infra bundles the runner-level proxy infrastructure that must exist BEFORE
// tool factories run: the egress State (source of truth), the shared capture
// FlowStore, and the long-lived MITM ProxyHub. Creating it up front lets the
// assembly set Deps.ScannerProxy to the stable hub address, so bash and every
// scanner engine route through the hub uniformly with no factory-order coupling.
type Infra struct {
	State *State
	Store *FlowStore
	Hub   *ProxyHub
}

// InstallInfra creates and starts the proxy infrastructure, points Deps at the
// hub (ScannerProxy + ScannerProxyCA), and stores the Infra in the bag for the
// proxy factory. The originating Deps.ScannerProxy becomes the hub's default
// upstream, so tool → hub → configured-proxy holds. On hub start failure it
// leaves Deps unchanged (tools fall back to the original proxy / direct) and
// still returns the Infra so the factory can register verbs against the State.
//
// capture selects the hub mode (see NewProxyHub): true records traffic (mitm
// on), false is a pure routing relay (mitm off). Routing works in both, so
// `proxy` keeps managing egress either way; only capture is gated.
func InstallInfra(d *commands.Deps, capture bool) (*Infra, error) {
	originalProxy := d.ScannerProxy
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

	store := NewFlowStore(10000)
	caRoot := filepath.Join(d.WorkDir, ".aiscan", "mitm")
	hub := NewProxyHub(state, store, caRoot, capture)

	infra := &Infra{State: state, Store: store, Hub: hub}
	commands.Provide(d, InfraKey, infra)

	if err := hub.Start(caRoot); err != nil {
		return infra, err
	}
	if hubURL := hub.ProxyURL(); hubURL != "" {
		d.ScannerProxy = hubURL
		d.ScannerProxyCA = hub.CAPath() // "" while not intercepting; no CA to trust
		// Resolve egress per execution from live hub state: tag the proxy URL
		// with the tool-call id so captured flows attribute to it, and read the
		// CA path fresh so it tracks runtime capture toggles.
		d.EgressResolver = func(callID string) (string, string) {
			return egressURL(hub.ProxyURL(), callID), hub.CAPath()
		}
	}
	return infra, nil
}

// egressURL inserts callID as the proxy username so the hub can attribute every
// captured flow on the connection to the originating tool-call. The value is
// percent-encoded by url.String and decoded back to callID by the HTTP client's
// Proxy-Authorization, so it round-trips even with unusual ids. An empty callID
// or base leaves the URL unchanged.
func egressURL(base, callID string) string {
	if base == "" || callID == "" {
		return base
	}
	u, err := url.Parse(base)
	if err != nil {
		return base
	}
	u.User = url.User(callID)
	return u.String()
}
