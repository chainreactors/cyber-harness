package node

import (
	"fmt"
	"net/url"
	"strings"

	aop "github.com/chainreactors/cyber/aop"
	coretool "github.com/chainreactors/cyber/core/tool"
)

// BuildHello builds the AOP core agent registration message.
func BuildHello(name string, executor coretool.Executor, nodeID string, runtimeInfo *aop.AgentRuntimeInfo) (*aop.AgentHello, error) {
	nodeID = strings.TrimSpace(nodeID)
	if nodeID == "" {
		return nil, fmt.Errorf("node_id is required")
	}
	if runtimeInfo == nil || runtimeInfo.Os == "" {
		runtimeInfo = DefaultRuntimeInfo()
	}
	hello := &aop.AgentHello{
		NodeId: nodeID, Name: name,
		Capabilities: []string{"repl", "pty", "tmux", "file", "exec", "sco"},
		Runtime:      runtimeInfo, Tools: executor.ToolDefinitions(),
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
