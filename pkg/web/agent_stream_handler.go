package web

import (
	"context"
	"encoding/json"
	"fmt"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	execpb "github.com/chainreactors/aiscan/aop/exec"
	filepb "github.com/chainreactors/aiscan/aop/file"
	ptypb "github.com/chainreactors/aiscan/aop/pty"
	scopb "github.com/chainreactors/aiscan/aop/sco"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/output"
	types "github.com/chainreactors/aiscan/pkg/types"
	terminalcodec "github.com/chainreactors/aiscan/pkg/web/terminal"
	"github.com/chainreactors/utils/pty"
	protobuf "google.golang.org/protobuf/proto"
)

func namespaceMessage[T protobuf.Message](message protobuf.Message) (T, error) {
	value, ok := message.(T)
	if !ok {
		var zero T
		return zero, fmt.Errorf("unexpected namespace message %T", message)
	}
	return value, nil
}

func (p *AgentPool) newAgentNamespaceMux(agent *remoteAgent) (*aop.NamespaceMux, error) {
	mux := aop.NewNamespaceMux()
	if err := mux.Register(&aop.ProtocolMessage{}, func(_ context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, err := namespaceMessage[*aop.ProtocolMessage](message)
		if err != nil {
			return err
		}
		p.handleAgentCoreMessage(agent, envelope, value)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := mux.Register(&types.CommandProtocolMessage{}, func(_ context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, err := namespaceMessage[*types.CommandProtocolMessage](message)
		if err != nil {
			return err
		}
		p.handleAgentCommandMessage(agent, envelope, value)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := mux.Register(&filepb.ProtocolMessage{}, func(_ context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, err := namespaceMessage[*filepb.ProtocolMessage](message)
		if err != nil {
			return err
		}
		p.handleAgentFileMessage(agent, envelope, value)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := mux.Register(&execpb.ProtocolMessage{}, func(_ context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, err := namespaceMessage[*execpb.ProtocolMessage](message)
		if err != nil {
			return err
		}
		p.handleAgentExecMessage(agent, envelope, value)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := mux.Register(&types.ReloadProtocolMessage{}, func(_ context.Context, _ *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, err := namespaceMessage[*types.ReloadProtocolMessage](message)
		if err != nil {
			return err
		}
		p.handleAgentReloadMessage(agent, value)
		return nil
	}); err != nil {
		return nil, err
	}
	if err := mux.Register(&ptypb.ProtocolMessage{}, func(_ context.Context, _ *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, err := namespaceMessage[*ptypb.ProtocolMessage](message)
		if err != nil {
			return err
		}
		p.forwardPTYFrame(terminalcodec.FromProto(value))
		return nil
	}); err != nil {
		return nil, err
	}
	if err := mux.Register(&toolpb.ProtocolMessage{}, func(_ context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, err := namespaceMessage[*toolpb.ProtocolMessage](message)
		if err != nil {
			return err
		}
		if progress := value.GetProgress(); progress != nil {
			p.handleToolProgress(envelope.ReplyTo, progress)
		}
		return nil
	}); err != nil {
		return nil, err
	}
	if err := mux.Register(&scopb.ProtocolMessage{}, func(ctx context.Context, envelope *aop.Envelope, message protobuf.Message, _ aop.SendFunc) error {
		value, err := namespaceMessage[*scopb.ProtocolMessage](message)
		if err != nil {
			return err
		}
		p.handleAgentSCOMessage(ctx, envelope, value)
		return nil
	}); err != nil {
		return nil, err
	}
	return mux, nil
}

func (p *AgentPool) handleAgentEnvelope(agent *remoteAgent, envelope *aop.Envelope) {
	mux, err := p.newAgentNamespaceMux(agent)
	if err != nil {
		return
	}
	p.dispatchAgentEnvelope(context.Background(), mux, envelope)
}

func (p *AgentPool) dispatchAgentEnvelope(ctx context.Context, mux *aop.NamespaceMux, envelope *aop.Envelope) {
	if mux == nil || envelope == nil {
		return
	}
	_, _ = mux.Dispatch(ctx, envelope, func(*aop.Envelope) error { return nil })
}

func (p *AgentPool) handleAgentCoreMessage(agent *remoteAgent, envelope *aop.Envelope, value *aop.ProtocolMessage) {
	if agent == nil || envelope == nil || value == nil {
		return
	}
	correlationID := envelope.ReplyTo
	switch payload := value.Message.(type) {
	case *aop.ProtocolMessage_AgentStatus:
		status := payload.AgentStatus
		if status == nil {
			return
		}
		agent.mu.Lock()
		if agent.status == nil {
			agent.status = &aop.AgentStatus{}
		}
		if status.Provider != "" {
			agent.status.Provider = status.Provider
		}
		if status.Model != "" {
			agent.status.Model = status.Model
		}
		agent.status.Bound = status.Bound
		agent.status.ConfigError = status.ConfigError
		if status.Space != "" {
			agent.status.Space = status.Space
		}
		agent.mu.Unlock()

	case *aop.ProtocolMessage_AgentStats:
		agent.mu.Lock()
		if payload.AgentStats == nil {
			agent.stats = &aop.AgentStats{}
		} else {
			agent.stats = protobuf.CloneOf(payload.AgentStats)
		}
		agent.mu.Unlock()

	case *aop.ProtocolMessage_OpenSessionResponse:
		response := payload.OpenSessionResponse
		if accepted := response.GetAccepted(); accepted != nil {
			agent.mu.Lock()
			agent.openSessions[accepted.Id] = struct{}{}
			agent.mu.Unlock()
		}
		result := taskResult{}
		if rejected := response.GetRejected(); rejected != nil {
			result.Err = rejected.Message
		}
		p.finishAgentTask(agent, correlationID, result)

	case *aop.ProtocolMessage_CloseSessionResponse:
		response := payload.CloseSessionResponse
		if accepted := response.GetAccepted(); accepted != nil {
			agent.mu.Lock()
			delete(agent.openSessions, accepted.Id)
			agent.mu.Unlock()
		}
		result := taskResult{}
		if rejected := response.GetRejected(); rejected != nil {
			result.Err = rejected.Message
		}
		p.finishAgentTask(agent, correlationID, result)

	case *aop.ProtocolMessage_RunTurnResponse:
		if rejected := payload.RunTurnResponse.GetRejected(); rejected != nil {
			p.finishAgentTask(agent, correlationID, taskResult{Err: rejected.Message})
		}

	case *aop.ProtocolMessage_CancelTurnResponse:
		// The local waiter is closed when cancellation is enqueued.

	case *aop.ProtocolMessage_Event:
		p.forwardAOPFrame(agent, correlationID, payload.Event)

	case *aop.ProtocolMessage_ProtocolError:
		if payload.ProtocolError != nil {
			p.finishAgentTask(agent, correlationID, taskResult{Err: payload.ProtocolError.Message})
		}
	}
}

func (p *AgentPool) handleAgentCommandMessage(agent *remoteAgent, envelope *aop.Envelope, value *types.CommandProtocolMessage) {
	if agent == nil || envelope == nil || value == nil {
		return
	}
	if catalog := value.GetCatalog(); catalog != nil {
		agent.mu.Lock()
		agent.commandsMenu = cloneCommandSpecs(catalog.Commands)
		agent.mu.Unlock()
		return
	}
	if result := value.GetResult(); result != nil {
		p.finishAgentTask(agent, envelope.ReplyTo, taskResult{})
	}
}

func (p *AgentPool) handleAgentFileMessage(agent *remoteAgent, envelope *aop.Envelope, value *filepb.ProtocolMessage) {
	if agent == nil || envelope == nil || value == nil {
		return
	}
	if result := value.GetResult(); result != nil {
		p.finishAgentTask(agent, envelope.ReplyTo, taskResult{File: protobuf.CloneOf(result)})
	}
}

func (p *AgentPool) handleAgentExecMessage(agent *remoteAgent, envelope *aop.Envelope, value *execpb.ProtocolMessage) {
	if agent == nil || envelope == nil || value == nil {
		return
	}
	if result := value.GetResult(); result != nil {
		p.finishAgentTask(agent, envelope.ReplyTo, taskResult{})
	}
	// Output is intentionally streaming-only and does not complete the task.
}

func (p *AgentPool) handleAgentReloadMessage(agent *remoteAgent, value *types.ReloadProtocolMessage) {
	if agent == nil || value == nil {
		return
	}
	result := value.GetResult()
	if result == nil {
		return
	}
	agent.mu.Lock()
	if agent.status == nil {
		agent.status = &aop.AgentStatus{}
	}
	if result.Ok {
		agent.status.Provider = result.Provider
		agent.status.Model = result.Model
		agent.status.ConfigError = ""
	} else {
		agent.status.ConfigError = result.Error
	}
	agent.mu.Unlock()
}

func (p *AgentPool) handleAgentSCOMessage(ctx context.Context, envelope *aop.Envelope, value *scopb.ProtocolMessage) {
	if envelope == nil || value == nil {
		return
	}
	nodes := value.GetNodes()
	if p.sco == nil || nodes == nil || len(nodes.Nodes) == 0 {
		return
	}
	values := make([]json.RawMessage, 0, len(nodes.Nodes))
	for _, node := range nodes.Nodes {
		values = append(values, append(json.RawMessage(nil), node...))
	}
	operationID := envelope.ReplyTo
	if operationID == "" {
		operationID = envelope.Id
	}
	_ = p.sco.UpsertSCONodes(ctx, operationID, values)
}

func (p *AgentPool) finishAgentTask(agent *remoteAgent, taskID string, result taskResult) {
	if agent == nil {
		return
	}
	agent.finishTask(taskID, result)
}

func (p *AgentPool) forwardAOPFrame(agent *remoteAgent, correlationID string, event *aop.Event) {
	if event == nil || event.SessionId == "" || event.Payload == nil {
		return
	}
	if p.sessions != nil {
		lookup := correlationID
		if event.TurnId != "" {
			lookup = event.TurnId
		}
		sessionID, ok := p.sessions.TaskSession(lookup)
		if !ok {
			switch event.Payload.(type) {
			case *aop.Event_SessionStarted, *aop.Event_SessionEnded:
				sessionID = event.SessionId
			default:
				// Session commands emit durable AOP messages without a turn ID:
				// they belong to the Runtime session, not to an LLM turn. The
				// agent connection also sends those events without reply_to, so
				// task correlation cannot resolve them. Accept the event only
				// when this exact agent has the Runtime session open; this keeps
				// standalone scan telemetry from leaking into chat history.
				if agent.state().sessionOpen(event.SessionId) {
					sessionID = event.SessionId
				} else {
					sessionID = ""
				}
			}
		}
		if sessionID != "" {
			p.sessions.BroadcastAOPEvent(sessionID, event)
		}
	}
	switch event.Payload.(type) {
	case *aop.Event_TurnEnded:
		agent.state().convergeOnTurnEnd(event.TurnId, event)
	case *aop.Event_ToolResult:
		agent.state().convergeOnToolResult(correlationID, event)
	}
}

func (p *AgentPool) handleToolProgress(operationID string, value *toolpb.Progress) {
	if value == nil {
		return
	}
	event := output.ToolDataEvent{Kind: output.ToolDataProgress, Data: value.Text, CallID: operationID}
	if value.Timestamp != nil {
		event.Timestamp = value.Timestamp.AsTime()
	} else {
		event.Timestamp = time.Now()
	}
	if event.Kind != output.ToolDataProgress || p.hub == nil || event.CallID == "" {
		return
	}
	line, ok := event.Data.(string)
	if !ok {
		return
	}
	line = output.StripANSI(line)
	if line != "" {
		p.hub.BroadcastScan(scanProgressEvent(event.CallID, line), false)
	}
}

func (p *AgentPool) forwardPTYFrame(frame pty.Frame) {
	if frame.StreamID == "" {
		return
	}
	p.ptyMu.RLock()
	ch := p.ptySubs[frame.StreamID]
	if ch != nil {
		select {
		case ch <- frame:
		default:
			p.ptyDrops.Add(1)
			select {
			case <-ch:
			default:
			}
			select {
			case ch <- frame:
			default:
				p.ptyDrops.Add(1)
			}
		}
	}
	p.ptyMu.RUnlock()
}
