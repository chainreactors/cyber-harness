package web

import (
	"context"
	"fmt"
	"sync"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	reloadpb "github.com/chainreactors/aiscan/pkg/types/reload"
	"github.com/chainreactors/ioa/protocols"
	"github.com/gorilla/websocket"
	protobuf "google.golang.org/protobuf/proto"
)

type webSocketEnvelopeStream struct {
	conn *websocket.Conn
	mu   sync.Mutex
}

func (s *webSocketEnvelopeStream) Recv() (*aop.Envelope, error) {
	_, data, err := s.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	envelope := new(aop.Envelope)
	if err := protobuf.Unmarshal(data, envelope); err != nil {
		return nil, fmt.Errorf("decode AOP envelope: %w", err)
	}
	return envelope, nil
}

func (s *webSocketEnvelopeStream) Send(envelope *aop.Envelope) error {
	data, err := protobuf.Marshal(envelope)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteMessage(websocket.BinaryMessage, data)
}

func (p *AgentPool) ServeAgentStream(ctx context.Context, stream aop.EnvelopeStream) error {
	if stream == nil {
		return fmt.Errorf("agent stream is required")
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	return p.serveAgentStream(ctx, stream, first)
}

func (p *AgentPool) serveAgentStream(parent context.Context, stream aop.EnvelopeStream, first *aop.Envelope) error {
	message, err := aop.Unwrap(first)
	if err != nil {
		return err
	}
	core, ok := message.(*aop.ProtocolMessage)
	if !ok || core.GetAgentHello() == nil {
		return fmt.Errorf("first AOP envelope must contain agent_hello")
	}
	hello := core.GetAgentHello()
	if hello.AgentId == "" || hello.Authority == "" {
		return fmt.Errorf("hello agent_id and authority are required")
	}
	node := protocols.NodeRef{ID: hello.AgentId, Authority: hello.Authority}
	nodeURI := canonicalNodeURI(hello.AgentId, hello.Authority)
	if nodeURI == "" {
		return fmt.Errorf("agent identity is required")
	}
	name := hello.Name
	if name == "" {
		name = "agent"
	}
	runtimeInfo := &aop.AgentRuntimeInfo{}
	if hello.Runtime != nil {
		runtimeInfo = protobuf.Clone(hello.Runtime).(*aop.AgentRuntimeInfo)
	}

	ctx, cancel := context.WithCancel(parent)
	agent := &remoteAgent{
		nodeURI: nodeURI, name: name, capabilities: append([]string(nil), hello.Capabilities...),
		close: cancel, sendCh: make(chan *aop.Envelope, 64),
		connectAt: time.Now(), node: node, runtime: runtimeInfo,
		status: &aop.AgentStatus{}, stats: &aop.AgentStats{},
		tasks: make(map[string]chan taskResult), turns: make(map[string]int), openSessions: make(map[string]struct{}),
		childSessions: make(map[string]map[string]struct{}), done: make(chan struct{}),
	}
	namespaceMux, err := p.newAgentNamespaceMux(agent)
	if err != nil {
		return fmt.Errorf("register agent namespaces: %w", err)
	}
	p.register(agent)
	defer func() {
		cancel()
		p.unregister(agent)
		close(agent.done)
	}()

	accepted, err := aop.Wrap(generateID(), first.Id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentAccepted{
		AgentAccepted: &aop.AgentAccepted{AgentId: hello.AgentId, Capabilities: append([]string(nil), hello.Capabilities...)},
	}})
	if err != nil {
		return err
	}
	if err := stream.Send(accepted); err != nil {
		return err
	}
	if p.config != nil {
		if config, configErr := p.config(ctx); configErr == nil && config != nil {
			reload, wrapErr := aop.Wrap(generateID(), "", &reloadpb.ProtocolMessage{Message: &reloadpb.ProtocolMessage_Request{Request: &reloadpb.Request{Config: config}}})
			if wrapErr != nil {
				return wrapErr
			}
			if err := stream.Send(reload); err != nil {
				return err
			}
		}
	}

	writeErr := make(chan error, 1)
	go func() {
		for {
			select {
			case envelope := <-agent.sendCh:
				if envelope == nil {
					continue
				}
				if err := stream.Send(envelope); err != nil {
					select {
					case writeErr <- err:
					default:
					}
					cancel()
					return
				}
			case <-ctx.Done():
				return
			}
		}
	}()

	recvCh := make(chan *aop.Envelope)
	recvErr := make(chan error, 1)
	go func() {
		for {
			envelope, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			select {
			case recvCh <- envelope:
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case envelope := <-recvCh:
			p.dispatchAgentEnvelope(ctx, namespaceMux, envelope)
		case err := <-recvErr:
			return err
		case err := <-writeErr:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
