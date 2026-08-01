package web

import (
	"context"
	"encoding/json"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"github.com/chainreactors/aiscan/core/output"
	terminalcodec "github.com/chainreactors/aiscan/pkg/web/terminal"
	"github.com/chainreactors/utils/pty"
	protobuf "google.golang.org/protobuf/proto"
)

func (p *AgentPool) handleAgentFrame(agent *remoteAgent, frame *transport.AgentFrame) {
	if agent == nil || frame == nil {
		return
	}
	switch payload := frame.Payload.(type) {
	case *transport.AgentFrame_Status:
		status := payload.Status
		if status == nil {
			return
		}
		agent.mu.Lock()
		if agent.status == nil {
			agent.status = &transport.AgentStatus{}
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

	case *transport.AgentFrame_Stats:
		agent.mu.Lock()
		if payload.Stats == nil {
			agent.stats = &transport.AgentStats{}
		} else {
			agent.stats = protobuf.Clone(payload.Stats).(*transport.AgentStats)
		}
		agent.mu.Unlock()

	case *transport.AgentFrame_OpenSession:
		if accepted := payload.OpenSession.GetAccepted(); accepted != nil {
			agent.mu.Lock()
			agent.openSessions[accepted.Id] = struct{}{}
			agent.mu.Unlock()
		}
		result := taskResult{}
		if rejected := payload.OpenSession.GetRejected(); rejected != nil {
			result.Err = rejected.Message
		}
		p.finishAgentTask(agent, frame.CorrelationId, result)

	case *transport.AgentFrame_CloseSession:
		if accepted := payload.CloseSession.GetAccepted(); accepted != nil {
			agent.mu.Lock()
			delete(agent.openSessions, accepted.Id)
			agent.mu.Unlock()
		}
		result := taskResult{}
		if rejected := payload.CloseSession.GetRejected(); rejected != nil {
			result.Err = rejected.Message
		}
		p.finishAgentTask(agent, frame.CorrelationId, result)

	case *transport.AgentFrame_RunTurn:
		if rejected := payload.RunTurn.GetRejected(); rejected != nil {
			p.finishAgentTask(agent, frame.CorrelationId, taskResult{Err: rejected.Message})
		}

	case *transport.AgentFrame_CancelTurn:
		// Cancellation is acknowledged by the response; the local waiter was
		// already closed when the cancel request was queued.

	case *transport.AgentFrame_Event:
		p.forwardAOPFrame(agent, frame.CorrelationId, payload.Event)

	case *transport.AgentFrame_CommandResult:
		result := payload.CommandResult
		if result != nil {
			p.finishAgentTask(agent, result.TaskId, taskResult{Result: append(json.RawMessage(nil), result.Result...)})
		}

	case *transport.AgentFrame_FileResult:
		result := payload.FileResult
		if result != nil {
			p.finishAgentTask(agent, result.TaskId, taskResult{File: protobuf.Clone(result).(*transport.FileResult)})
		}

	case *transport.AgentFrame_ExecOutput:
		// Exec output is streaming telemetry. Callers that need it consume the
		// terminal result; no second output envelope is maintained.

	case *transport.AgentFrame_ExecResult:
		result := payload.ExecResult
		if result != nil {
			encoded, _ := json.Marshal(result)
			p.finishAgentTask(agent, result.TaskId, taskResult{Result: encoded})
		}

	case *transport.AgentFrame_OperationError:
		failure := payload.OperationError
		if failure != nil {
			p.finishAgentTask(agent, failure.TaskId, taskResult{Err: failure.Message})
		}

	case *transport.AgentFrame_ConfigReload:
		result := payload.ConfigReload
		if result == nil {
			return
		}
		agent.mu.Lock()
		if result.Ok {
			agent.status.Provider = result.Provider
			agent.status.Model = result.Model
			agent.status.ConfigError = ""
		} else {
			agent.status.ConfigError = result.Error
		}
		agent.mu.Unlock()

	case *transport.AgentFrame_Terminal:
		if payload.Terminal != nil {
			p.forwardPTYFrame(terminalcodec.FromProto(payload.Terminal))
		}

	case *transport.AgentFrame_ToolTelemetry:
		p.handleToolTelemetry(agent, payload.ToolTelemetry)

	case *transport.AgentFrame_ScoNodes:
		if p.sco != nil && payload.ScoNodes != nil && len(payload.ScoNodes.Nodes) > 0 {
			nodes := make([]json.RawMessage, 0, len(payload.ScoNodes.Nodes))
			for _, node := range payload.ScoNodes.Nodes {
				nodes = append(nodes, append(json.RawMessage(nil), node...))
			}
			scanID := payload.ScoNodes.CallId
			if scanID == "" {
				scanID = frame.CorrelationId
			}
			if scanID == "" {
				scanID = "standalone"
			}
			_ = p.sco.UpsertSCONodes(context.Background(), scanID, nodes)
		}
	}
}

func (p *AgentPool) finishAgentTask(agent *remoteAgent, taskID string, result taskResult) {
	if agent == nil || taskID == "" {
		return
	}
	agent.mu.Lock()
	ch, ok := agent.tasks[taskID]
	result.Turn = agent.turns[taskID]
	if ok {
		delete(agent.tasks, taskID)
		delete(agent.turns, taskID)
		delete(agent.toolCalls, taskID)
		delete(agent.childSessions, taskID)
	}
	agent.mu.Unlock()
	if ok && ch != nil {
		ch <- result
		close(ch)
	}
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
			// Session lifecycle frames are not part of a turn and therefore may
			// legitimately arrive without a task correlation. Other uncorrelated
			// AOP frames can belong to standalone scans and must not leak into the
			// chat transcript merely because their scan ID occupies session_id.
			switch event.Payload.(type) {
			case *aop.Event_SessionStarted, *aop.Event_SessionEnded:
				sessionID = event.SessionId
			default:
				sessionID = ""
			}
		}
		if sessionID != "" {
			p.sessions.BroadcastAOPEvent(sessionID, event)
		}
	}
	switch event.Payload.(type) {
	case *aop.Event_TurnEnded:
		p.convergeTaskOnTurnEnd(agent, event.TurnId, event)
	case *aop.Event_ToolResult:
		p.convergeTaskOnToolResult(agent, correlationID, event)
	}
}

func (p *AgentPool) handleToolTelemetry(agent *remoteAgent, value *transport.ToolTelemetry) {
	if value == nil {
		return
	}
	var data any
	if value.Data != nil {
		data, _ = aop.DecodeJSON[any](value.Data)
	}
	event := output.ToolDataEvent{
		Tool: value.Tool, Kind: value.Kind, Target: value.Target, Data: data, CallID: value.CallId,
	}
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
	if line == "" {
		return
	}
	p.hub.BroadcastScan(scanProgressEvent(event.CallID, line), false)
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
