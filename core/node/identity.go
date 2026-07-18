package node

import (
	"net/url"
	"os"
	"os/user"
	"runtime"
	"strings"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

// DefaultIdentity returns a basic OS-based agent identity without any
// runner.AgentRuntime dependency.
func DefaultIdentity() webproto.AgentIdentity {
	identity := webproto.AgentIdentity{
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		PID:          os.Getpid(),
		Capabilities: []string{"repl", "pty", "tmux", "ioa"},
		Meta:         map[string]any{"client": "aiscan", "transport": "web-agent"},
	}
	if host, err := os.Hostname(); err == nil {
		identity.Hostname = host
	}
	if wd, err := os.Getwd(); err == nil {
		identity.WorkingDir = wd
	}
	if current, err := user.Current(); err == nil && current != nil {
		identity.Username = current.Username
	}
	return identity
}

// RegisterPayload builds the WebSocket registration payload.
func RegisterPayload(name string, reg *commands.CommandRegistry, identityFn func() webproto.AgentIdentity, menuFn func() []webproto.CommandSpec, stats webproto.AgentStats) webproto.RegisterPayload {
	var identity webproto.AgentIdentity
	if identityFn != nil {
		identity = identityFn()
	} else {
		identity = DefaultIdentity()
	}

	var menu []webproto.CommandSpec
	if menuFn != nil {
		menu = menuFn()
	}

	payload := webproto.RegisterPayload{
		Name:         name,
		Commands:     reg.Names(),
		CommandsMenu: menu,
		Stats:        stats,
		Identity:     identity,
	}
	if payload.Identity.NodeName == "" {
		payload.Identity.NodeName = name
	}
	return payload
}

// SplitAccessKey lifts the access token out of a URL's userinfo
// (http://<token>@host...), returning a userinfo-free URL plus the token.
// A URL without userinfo (or an unparseable one) comes back unchanged
// with an empty token.
func SplitAccessKey(rawURL string) (dialURL, token string) {
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		return rawURL, ""
	}
	token = u.User.Username()
	u.User = nil
	return u.String(), token
}

// HTTPToWS converts an HTTP(S) URL to a WS(S) URL.
func HTTPToWS(rawURL string) string {
	u, err := url.Parse(strings.TrimRight(rawURL, "/"))
	if err != nil {
		return rawURL
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	return u.String()
}

// PublicIOAURL strips userinfo from an IOA URL for safe display.
func PublicIOAURL(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil {
		return raw
	}
	parsed.User = nil
	return parsed.String()
}
