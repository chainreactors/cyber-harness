package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/exts/guardrail"
	"google.golang.org/protobuf/proto"
)

// forwardGuardrail uses the control channel directly. Sending a Session command
// here would queue behind the invocation that is waiting for this approval.
func (s *Service) forwardGuardrail(ctx context.Context, request *guardrail.ProtocolMessage) (*guardrail.ProtocolMessage, error) {
	var sessionID string
	switch {
	case request.GetPending() != nil:
		sessionID = request.GetPending().SessionId
	case request.GetResolve() != nil:
		sessionID = request.GetResolve().SessionId
	default:
		return nil, fmt.Errorf("pending or resolve request required")
	}
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("review session not found")
	}
	if s.agents == nil {
		return nil, fmt.Errorf("agent pool unavailable")
	}
	nodeID := session.GetSession().GetNodeId()
	if nodeID == "" {
		return nil, fmt.Errorf("session has no assigned node")
	}
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	id := aop.EnvelopeID()
	result, err := s.agents.dispatchMessage(nodeID, id, proto.Clone(request))
	if err != nil {
		return nil, err
	}
	// Drop only the transport waiter on cancellation. Never cancel the tool or
	// submit another invocation as a consequence of a disconnected approval UI.
	defer func() {
		if agent := s.agents.get(nodeID); agent != nil {
			agent.dropTask(id)
		}
	}()
	select {
	case reply, ok := <-result:
		if !ok {
			return nil, errors.New("agent disconnected during guardrail request")
		}
		if failure := taskError(reply); failure != nil {
			return nil, errors.New(failure.Message)
		}
		response, _ := reply.(*guardrail.ProtocolMessage)
		if response == nil {
			return nil, errors.New("missing guardrail response")
		}
		return response, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
