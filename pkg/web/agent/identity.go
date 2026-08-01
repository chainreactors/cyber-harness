package agent

import (
	"fmt"
	"net/url"
	"os"
	"os/user"
	"runtime"
	"strings"

	aop "github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/ioa/protocols"
)

// DefaultRuntime returns OS process metadata without introducing another
// identity beside the IOA NodeRef.
func DefaultRuntime() *transport.AgentRuntimeInfo {
	metadata, _ := aop.JSONValue(map[string]any{"client": "aiscan", "transport": "websocket"})
	runtimeInfo := &transport.AgentRuntimeInfo{
		Os:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		Pid:          int32(os.Getpid()),
		Capabilities: []string{"repl", "pty", "tmux", "ioa"},
		Metadata:     metadata,
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

// BuildHello builds the transport-native agent registration frame.
func BuildHello(name string, reg *commands.CommandRegistry, ref protocols.NodeRef, runtimeInfo *transport.AgentRuntimeInfo, statusFn func() *transport.AgentStatus, menuFn func() []*transport.CommandSpec, stats *transport.AgentStats) (*transport.AgentHello, error) {
	if !ref.Valid() {
		return nil, fmt.Errorf("valid node reference is required")
	}
	if runtimeInfo == nil || runtimeInfo.Os == "" {
		runtimeInfo = DefaultRuntime()
	}
	var status *transport.AgentStatus
	if statusFn != nil {
		status = statusFn()
	}
	if status == nil {
		status = &transport.AgentStatus{}
	}
	if stats == nil {
		stats = &transport.AgentStats{}
	}
	var menu []*transport.CommandSpec
	if menuFn != nil {
		menu = menuFn()
	}
	hello := &transport.AgentHello{
		AgentId: ref.ID, Authority: ref.Authority, Name: name,
		Commands: reg.Names(), CommandMenu: menu, Runtime: runtimeInfo, Status: status, Stats: stats,
	}
	for _, definition := range reg.ToolDefinitions() {
		schema, _ := aop.JSONValue(definition.Function.Parameters)
		hello.Tools = append(hello.Tools, &transport.ToolDefinition{
			Type: definition.Type, Name: definition.Function.Name,
			Description: definition.Function.Description, InputSchema: schema,
		})
	}
	return hello, nil
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
