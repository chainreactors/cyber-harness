package webagent

import (
	"fmt"
	"net/url"
	"os"
	"os/user"
	"runtime"
	"strings"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/webproto"
	"github.com/chainreactors/ioa/protocols"
)

// DefaultRuntime returns OS process metadata without introducing another
// identity beside the IOA NodeRef.
func DefaultRuntime() webproto.AgentRuntime {
	runtimeInfo := webproto.AgentRuntime{
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		PID:          os.Getpid(),
		Capabilities: []string{"repl", "pty", "tmux", "ioa"},
		Meta:         map[string]any{"client": "aiscan", "transport": "websocket"},
	}
	if host, err := os.Hostname(); err == nil {
		runtimeInfo.Hostname = host
	}
	if wd, err := os.Getwd(); err == nil {
		runtimeInfo.WorkingDir = wd
	}
	if current, err := user.Current(); err == nil && current != nil {
		runtimeInfo.Username = current.Username
	}
	return runtimeInfo
}

// RegisterPayload builds the WebSocket registration payload.
func RegisterPayload(name string, reg *commands.CommandRegistry, ref protocols.NodeRef, runtimeInfo webproto.AgentRuntime, statusFn func() webproto.AgentStatus, menuFn func() []webproto.CommandSpec, stats webproto.AgentStats) (webproto.RegisterPayload, error) {
	if !ref.Valid() {
		return webproto.RegisterPayload{}, fmt.Errorf("valid node reference is required")
	}
	if runtimeInfo.OS == "" {
		runtimeInfo = DefaultRuntime()
	}
	var status webproto.AgentStatus
	if statusFn != nil {
		status = statusFn()
	}

	var menu []webproto.CommandSpec
	if menuFn != nil {
		menu = menuFn()
	}

	payload := webproto.RegisterPayload{
		Name:         name,
		Commands:     reg.Names(),
		Tools:        reg.ToolDefinitions(),
		CommandsMenu: menu,
		Stats:        stats,
		Node:         ref,
		Runtime:      runtimeInfo,
		Status:       status,
	}
	return payload, nil
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
