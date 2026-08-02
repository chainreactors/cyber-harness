package agent

import (
	"fmt"
	"net/url"
	"os"
	"os/user"
	"runtime"
	"strings"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/ioa/protocols"
	"google.golang.org/protobuf/types/known/structpb"
)

// DefaultRuntime returns OS process metadata without introducing another
// identity beside the IOA NodeRef.
func DefaultRuntime() *aop.AgentRuntimeInfo {
	metadata, _ := structpb.NewStruct(map[string]any{"client": "aiscan", "transport": "websocket"})
	runtimeInfo := &aop.AgentRuntimeInfo{
		Os:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Pid:      int32(os.Getpid()),
		Metadata: metadata,
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

// BuildHello builds the AOP core agent registration message.
func BuildHello(name string, reg *commands.CommandRegistry, ref protocols.NodeRef, runtimeInfo *aop.AgentRuntimeInfo) (*aop.AgentHello, error) {
	if !ref.Valid() {
		return nil, fmt.Errorf("valid node reference is required")
	}
	if runtimeInfo == nil || runtimeInfo.Os == "" {
		runtimeInfo = DefaultRuntime()
	}
	hello := &aop.AgentHello{
		AgentId: ref.ID, Authority: ref.Authority, Name: name,
		Capabilities: []string{"repl", "pty", "tmux", "ioa", "file", "exec", "sco"},
		Runtime:      runtimeInfo, Tools: reg.ToolDefinitions(),
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
