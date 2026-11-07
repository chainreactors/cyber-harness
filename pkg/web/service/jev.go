package service

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/exts/jev"
	"google.golang.org/protobuf/proto"
)

// A library query uses the bound node's control channel and never queues an
// agent turn, executes a tool, or starts compilation.
func (s *Service) forwardJEV(ctx context.Context, request *jev.ProtocolMessage) (*jev.ProtocolMessage, error) {
	sessionID := request.GetRequest().GetSessionId()
	if wait := request.GetWaitIdle(); wait != nil {
		sessionID = wait.SessionId
	}
	if sessionID == "" {
		return nil, fmt.Errorf("session_id is required")
	}
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		return nil, fmt.Errorf("session not found")
	}
	if s.agents == nil {
		return nil, fmt.Errorf("agent pool unavailable")
	}
	nodeID := session.GetSession().GetNodeId()
	if nodeID == "" {
		return nil, fmt.Errorf("session has no assigned node")
	}
	timeout := 10 * time.Second
	if wait := request.GetWaitIdle(); wait != nil {
		timeout = 6*time.Minute + time.Second
		if wait.TimeoutMs > 0 && int64(wait.TimeoutMs) < (6*time.Minute).Milliseconds() {
			timeout = time.Duration(wait.TimeoutMs)*time.Millisecond + time.Second
		}
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	id := aop.EnvelopeID()
	result, err := s.agents.dispatchMessage(nodeID, id, proto.Clone(request))
	if err != nil {
		return nil, err
	}
	defer func() {
		if agent := s.agents.get(nodeID); agent != nil {
			agent.dropTask(id)
		}
	}()
	select {
	case reply, ok := <-result:
		if !ok {
			return nil, errors.New("agent disconnected during JEV query")
		}
		if failure := taskError(reply); failure != nil {
			return nil, errors.New(failure.Message)
		}
		response, _ := reply.(*jev.ProtocolMessage)
		if response == nil || (request.GetWaitIdle() != nil && response.GetIdle() == nil) || (request.GetRequest() != nil && response.GetLibrary() == nil) {
			return nil, errors.New("missing JEV library response")
		}
		return response, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}
