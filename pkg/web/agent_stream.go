package web

import (
	"context"
	"fmt"
	"net/http"
	"sync"
	"time"

	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"github.com/chainreactors/ioa/protocols"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/encoding/protojson"
	protobuf "google.golang.org/protobuf/proto"
)

type ServerAgentStream interface {
	Context() context.Context
	Recv() (*transport.AgentFrame, error)
	Send(*transport.ServerFrame) error
}

type agentTransportServer struct {
	transport.UnimplementedAgentTransportServiceServer
	pool *AgentPool
}

func NewAgentTransportServer(pool *AgentPool) transport.AgentTransportServiceServer {
	return &agentTransportServer{pool: pool}
}

func (s *agentTransportServer) Connect(stream transport.AgentTransportService_ConnectServer) error {
	if s.pool == nil {
		return fmt.Errorf("agent pool is unavailable")
	}
	return s.pool.ServeAgentStream(stream)
}

type webSocketAgentStream struct {
	ctx  context.Context
	conn *websocket.Conn
	mu   sync.Mutex
}

func (s *webSocketAgentStream) Context() context.Context { return s.ctx }

func (s *webSocketAgentStream) Recv() (*transport.AgentFrame, error) {
	_, data, err := s.conn.ReadMessage()
	if err != nil {
		return nil, err
	}
	frame := new(transport.AgentFrame)
	if err := protojson.Unmarshal(data, frame); err != nil {
		return nil, fmt.Errorf("decode agent frame: %w", err)
	}
	return frame, nil
}

func (s *webSocketAgentStream) Send(frame *transport.ServerFrame) error {
	data, err := protojson.Marshal(frame)
	if err != nil {
		return err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	return s.conn.WriteMessage(websocket.TextMessage, data)
}

func (p *AgentPool) HandleWS(w http.ResponseWriter, r *http.Request) {
	conn, err := p.upgrader.Upgrade(w, r, nil)
	if err != nil {
		return
	}
	defer conn.Close()
	_ = p.ServeAgentStream(&webSocketAgentStream{ctx: r.Context(), conn: conn})
}

func (p *AgentPool) ServeAgentStream(stream ServerAgentStream) error {
	if stream == nil {
		return fmt.Errorf("agent stream is required")
	}
	first, err := stream.Recv()
	if err != nil {
		return err
	}
	hello := first.GetHello()
	if hello == nil {
		return fmt.Errorf("first agent frame must contain hello")
	}
	if hello.AgentId == "" || hello.Authority == "" {
		return fmt.Errorf("hello agent_id and authority are required")
	}
	node := protocols.NodeRef{ID: hello.AgentId, Authority: hello.Authority}
	id := agentKey(hello.AgentId, hello.Authority)
	if id == "" {
		return fmt.Errorf("agent identity is required")
	}
	name := hello.Name
	if name == "" {
		name = "agent"
	}
	runtimeInfo := &transport.AgentRuntimeInfo{}
	if hello.Runtime != nil {
		runtimeInfo = protobuf.Clone(hello.Runtime).(*transport.AgentRuntimeInfo)
	}
	statusValue := &transport.AgentStatus{}
	if hello.Status != nil {
		statusValue = protobuf.Clone(hello.Status).(*transport.AgentStatus)
	}
	statsValue := &transport.AgentStats{}
	if hello.Stats != nil {
		statsValue = protobuf.Clone(hello.Stats).(*transport.AgentStats)
	}

	ctx, cancel := context.WithCancel(stream.Context())
	agent := &remoteAgent{
		id: id, name: name, commands: append([]string(nil), hello.Commands...), commandsMenu: cloneCommandSpecs(hello.CommandMenu),
		close: cancel, sendCh: make(chan *transport.ServerFrame, 32), controlCh: make(chan *transport.ServerFrame, 32),
		connectAt: time.Now(), node: node, runtime: runtimeInfo, status: statusValue, stats: statsValue,
		tasks: make(map[string]chan taskResult), turns: make(map[string]int), openSessions: make(map[string]struct{}),
		childSessions: make(map[string]map[string]struct{}), done: make(chan struct{}),
	}
	p.register(agent)
	defer func() {
		cancel()
		p.unregister(agent)
		close(agent.done)
	}()

	if err := stream.Send(&transport.ServerFrame{Payload: &transport.ServerFrame_Accepted{Accepted: &transport.ConnectionAccepted{
		AgentId: agent.id, Name: agent.name, Capabilities: agent.runtime.GetCapabilities(),
	}}}); err != nil {
		return err
	}

	writeErr := make(chan error, 1)
	go func() {
		for {
			var frame *transport.ServerFrame
			select {
			case frame = <-agent.controlCh:
			default:
				select {
				case frame = <-agent.controlCh:
				case frame = <-agent.sendCh:
				case <-ctx.Done():
					return
				}
			}
			if frame == nil {
				continue
			}
			if frame.GetReloadConfig() != nil {
				agent.finishConfigReload()
			}
			if err := stream.Send(frame); err != nil {
				select {
				case writeErr <- err:
				default:
				}
				cancel()
				return
			}
		}
	}()

	recvCh := make(chan *transport.AgentFrame)
	recvErr := make(chan error, 1)
	go func() {
		for {
			frame, err := stream.Recv()
			if err != nil {
				recvErr <- err
				return
			}
			select {
			case recvCh <- frame:
			case <-ctx.Done():
				return
			}
		}
	}()

	for {
		select {
		case frame := <-recvCh:
			p.handleAgentFrame(agent, frame)
		case err := <-recvErr:
			return err
		case err := <-writeErr:
			return err
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
