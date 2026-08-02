package web

import (
	ptypb "github.com/chainreactors/aiscan/aop/pty"
	managementapi "github.com/chainreactors/aiscan/pkg/web/api"
)

// ptyRouter adapts the AgentPool PTY mechanisms to the api.PTYRouter
// definition. PTY messages remain canonical AOP protobuf end to end.
type ptyRouter struct {
	pool *AgentPool
}

func (r ptyRouter) Subscribe(nodeID, streamID string) (<-chan *ptypb.ProtocolMessage, bool, func()) {
	return r.pool.subscribePTY(nodeID, streamID)
}

func (r ptyRouter) Forward(nodeID string, message *ptypb.ProtocolMessage) error {
	return r.pool.sendAgentMessage(nodeID, generateID(), "", message)
}

func (r ptyRouter) Close(nodeID, streamID string) {
	r.pool.CloseTerminal(nodeID, streamID)
}

var (
	_ managementapi.PTYRouter       = ptyRouter{}
	_ managementapi.CommandExecutor = (*Service)(nil)
	_ managementapi.FileUploader    = (*Service)(nil)
)
