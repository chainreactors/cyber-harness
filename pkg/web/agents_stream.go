package web

import (
	"context"
	"fmt"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	types "github.com/chainreactors/aiscan/pkg/types"
	protobuf "google.golang.org/protobuf/proto"
)

// ServeNode owns the Node Endpoint handshake and AgentPool registration. Once
// initialized, node traffic uses the same Connection runtime as applications.
func (p *AgentPool) ServeNode(parent context.Context, stream aop.EnvelopeStream) error {
	if p == nil || stream == nil {
		return fmt.Errorf("node AOP stream is unavailable")
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	message, err := aop.Unwrap(first)
	if err != nil {
		return err
	}
	core, ok := message.(*aop.ProtocolMessage)
	if !ok || core.GetAgentHello() == nil {
		return fmt.Errorf("first node AOP envelope must contain AgentHello")
	}
	hello := core.GetAgentHello()
	if hello.NodeId == "" {
		return fmt.Errorf("AgentHello node_id is required")
	}

	connection, err := NewConnection(parent, stream)
	if err != nil {
		return err
	}
	defer connection.Close()
	ctx := connection.Context()

	name := hello.Name
	if name == "" {
		name = "agent"
	}
	runtimeInfo := &aop.AgentRuntimeInfo{}
	if hello.Runtime != nil {
		runtimeInfo = protobuf.CloneOf(hello.Runtime)
	}
	agent := &remoteAgent{
		nodeState:    newNodeState(),
		nodeID:       hello.NodeId,
		name:         name,
		capabilities: append([]string(nil), hello.Capabilities...),
		close:        connection.Close,
		send:         connection.Send,
		connectAt:    time.Now(),
		runtime:      runtimeInfo,
		status:       &aop.AgentStatus{},
		stats:        &aop.AgentStats{},
		done:         make(chan struct{}),
	}
	namespaceMux, err := p.newAgentNamespaceMux(agent)
	if err != nil {
		return fmt.Errorf("register node namespaces: %w", err)
	}
	accepted, err := aop.Wrap(generateID(), first.Id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentAccepted{
		AgentAccepted: &aop.AgentAccepted{NodeId: hello.NodeId, Capabilities: append([]string(nil), hello.Capabilities...)},
	}})
	if err != nil {
		return err
	}
	if err := connection.Send(accepted); err != nil {
		return err
	}
	if p.config != nil {
		if config, configErr := p.config(ctx); configErr == nil && config != nil {
			reload, wrapErr := aop.Wrap(generateID(), "", &types.ReloadProtocolMessage{Message: &types.ReloadProtocolMessage_Request{Request: &types.ReloadRequest{Config: config}}})
			if wrapErr != nil {
				return wrapErr
			}
			if err := connection.Send(reload); err != nil {
				return err
			}
		}
	}
	p.register(agent)
	defer func() {
		p.unregister(agent)
		close(agent.done)
	}()

	dispatch := func(dispatchCtx context.Context, envelope *aop.Envelope, send aop.SendFunc) error {
		handled, dispatchErr := namespaceMux.Dispatch(dispatchCtx, envelope, send)
		if dispatchErr != nil {
			return dispatchErr
		}
		if !handled {
			return fmt.Errorf("unsupported node AOP namespace")
		}
		return nil
	}
	return connection.Run(nil, dispatch)
}
