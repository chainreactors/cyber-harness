package service

import (
	"context"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	managementapi "github.com/chainreactors/aiscan/pkg/web/api"
	"google.golang.org/protobuf/proto"
)

const agentControlTimeout = 10 * time.Second

func (s *Service) AgentInfo(nodeID string) (string, bool) {
	if s == nil || s.agents == nil {
		return "", false
	}
	agent := s.agents.get(nodeID)
	if agent == nil {
		return "", false
	}
	return agent.Name(), true
}

func (s *Service) OpenAgentSession(ctx context.Context, requestID string, request *aop.OpenSessionRequest) error {
	if s == nil || s.agents == nil || request == nil {
		return managementapi.Errorf(managementapi.CodeUnavailable, "node is not connected")
	}
	if s.agents.SessionOpen(request.NodeId, request.SessionId) {
		return nil
	}
	resultCh, err := s.agents.DispatchOpenSession(request.NodeId, requestID, proto.CloneOf(request))
	if err != nil {
		return managementapi.NewError(managementapi.CodeUnavailable, err)
	}
	timer := time.NewTimer(agentControlTimeout)
	defer timer.Stop()
	select {
	case result, ok := <-resultCh:
		if ok && result.Err == "" {
			return nil
		}
		message := result.Err
		if message == "" {
			message = "node disconnected while opening session"
		}
		return managementapi.Errorf(managementapi.CodeFailedPrecondition, "%s", message)
	case <-ctx.Done():
		return ctx.Err()
	case <-timer.C:
		return managementapi.Errorf(managementapi.CodeUnavailable, "node timed out while opening session")
	}
}

func (s *Service) CloseAgentSession(ctx context.Context, requestID, nodeID string, request *aop.CloseSessionRequest) (bool, error) {
	if s == nil || request == nil {
		return false, nil
	}
	if _, connected := s.AgentInfo(nodeID); !connected {
		return false, nil
	}
	resultCh, err := s.agents.DispatchCloseSession(nodeID, requestID, proto.CloneOf(request))
	if err != nil {
		return true, managementapi.NewError(managementapi.CodeUnavailable, err)
	}
	timer := time.NewTimer(agentControlTimeout)
	defer timer.Stop()
	select {
	case result, ok := <-resultCh:
		if ok && result.Err == "" {
			return true, nil
		}
		message := result.Err
		if message == "" {
			message = "node disconnected while closing session"
		}
		return true, managementapi.Errorf(managementapi.CodeFailedPrecondition, "%s", message)
	case <-ctx.Done():
		return true, ctx.Err()
	case <-timer.C:
		return true, managementapi.Errorf(managementapi.CodeUnavailable, "node timed out while closing session")
	}
}

func (s *Service) StartAgentTurn(sessionID string, request *aop.RunTurnRequest) {
	s.handleAgentRun(sessionID, request)
}

func (s *Service) CancelAgentTurn(ctx context.Context, sessionID, turnID string) error {
	return s.CancelTurn(ctx, sessionID, turnID)
}

func (s *Service) PublishUserMessage(sessionID, turnID string, message *aop.Message) {
	s.publishUserMessage(sessionID, turnID, message)
}

func (s *Service) PublishSessionEvent(sessionID string, event *aop.Event) {
	s.BroadcastAOPEvent(sessionID, event)
}

func (s *Service) SubscribeSessionEvents(sessionID string) (<-chan *aop.EventDelivery, func()) {
	if s == nil || s.hub == nil {
		closed := make(chan *aop.EventDelivery)
		close(closed)
		return closed, func() {}
	}
	return s.hub.SubscribeAOP(sessionID)
}

var _ managementapi.SessionRuntime = (*Service)(nil)
