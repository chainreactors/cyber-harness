package node

import (
	"fmt"
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
		Capabilities: []string{"repl", "pty", "tmux", "file", "sco"},
		Runtime:      runtimeInfo, Tools: executor.ToolDefinitions(),
	}
	return hello, nil
}
