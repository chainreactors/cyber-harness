// Package egress declares how outbound traffic is routed, as the tools that
// make outbound calls see it.
//
// The interface lives here rather than beside the proxy implementation so that
// a build without a proxy can still satisfy it: a minimal host provides
// Disabled and links no proxy at all.
package egress

import "context"

// Endpoint is the routing decision for outbound calls.
type Endpoint interface {
	// ProxyURL is the upstream to route through, or empty for a direct call.
	ProxyURL() string
	// CAPath is the certificate authority to trust for intercepted traffic.
	CAPath() string
	// Egress scopes one call's routing and returns the release for it.
	Egress(context.Context) (string, string, func())
}

// Disabled is the endpoint of a host that routes nothing. A profile without a
// proxy provides this, so consumers keep one code path instead of each growing
// a nil check.
func Disabled() Endpoint { return disabled{} }

type disabled struct{}

func (disabled) ProxyURL() string { return "" }

func (disabled) CAPath() string { return "" }

func (disabled) Egress(context.Context) (string, string, func()) { return "", "", func() {} }
