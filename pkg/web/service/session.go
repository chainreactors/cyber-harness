package service

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	filepb "github.com/chainreactors/aiscan/aop/file"
	managementapi "github.com/chainreactors/aiscan/pkg/web/api"
	"google.golang.org/protobuf/proto"
)

const (
	agentControlTimeout = 10 * time.Second
	SessionStateOpen    = managementapi.SessionStateOpen
	SessionStateClosed  = managementapi.SessionStateClosed
)

var (
	ErrSessionNotFound = errors.New("session not found")
	ErrTurnNotFound    = managementapi.ErrTurnNotFound
)

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

func (s *Service) SubscribeSessionEvents(sessionID string) (<-chan *aop.EventDelivery, func()) {
	if s == nil || s.hub == nil {
		closed := make(chan *aop.EventDelivery)
		close(closed)
		return closed, func() {}
	}
	return s.hub.SubscribeAOP(sessionID)
}

var _ managementapi.SessionRuntime = (*Service)(nil)

func (s *Service) TaskSession(taskID string) (string, bool) {
	s.mu.Lock()
	defer s.mu.Unlock()
	sid, ok := s.taskSessions[taskID]
	return sid, ok
}

func (s *Service) registerSessionTask(taskID, sessionID, nodeID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.taskSessions[taskID] = sessionID
	if nodeID != "" {
		s.taskNodeIDs[taskID] = nodeID
	}
	delete(s.taskCanceled, taskID)
}

func (s *Service) finishSessionTask(taskID string) bool {
	s.mu.Lock()
	defer s.mu.Unlock()
	canceled := s.taskCanceled[taskID]
	delete(s.taskSessions, taskID)
	delete(s.taskNodeIDs, taskID)
	delete(s.taskCanceled, taskID)
	return canceled
}

func (s *Service) CancelTurn(ctx context.Context, sessionID, turnID string) error {
	if _, err := s.store.GetSession(ctx, sessionID); err != nil {
		return err
	}
	turnID = strings.TrimSpace(turnID)
	if turnID == "" {
		return ErrTurnNotFound
	}
	s.mu.Lock()
	sid, pending := s.taskSessions[turnID]
	nodeID := s.taskNodeIDs[turnID]
	if pending && sid == sessionID {
		s.taskCanceled[turnID] = true
	} else {
		pending = false
	}
	s.mu.Unlock()
	if !pending {
		return ErrTurnNotFound
	}
	if s.agents != nil && nodeID != "" {
		if err := s.agents.CancelTask(nodeID, turnID, sessionID); err != nil {
			return err
		}
	}
	s.BroadcastAOPEvent(sessionID, &aop.Event{
		SessionId: sessionID, TurnId: turnID, Emitter: "aiscan.web",
		Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{StopReason: "canceled"}},
	})
	return nil
}

func (s *Service) Upload(ctx context.Context, sessionID, filename string, data []byte) (*filepb.Result, error) {
	session, err := s.store.GetSession(ctx, sessionID)
	if err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return nil, fmt.Errorf("%w: %s", ErrSessionNotFound, sessionID)
		}
		return nil, fmt.Errorf("get upload session: %w", err)
	}
	if s.agents == nil {
		return nil, fmt.Errorf("no agent pool available")
	}
	nodeID := session.GetSession().GetNodeId()
	if nodeID == "" {
		return nil, fmt.Errorf("session has no assigned node")
	}

	taskID := generateID()
	resultCh, err := s.agents.dispatchMessage(nodeID, taskID, &filepb.ProtocolMessage{Message: &filepb.ProtocolMessage_UploadRequest{UploadRequest: &filepb.UploadRequest{
		SessionId: sessionID, Filename: filename, MediaType: http.DetectContentType(data), Data: data,
	}}})
	if err != nil {
		return nil, fmt.Errorf("agent dispatch failed: %w", err)
	}

	select {
	case res, ok := <-resultCh:
		if !ok {
			return nil, fmt.Errorf("agent disconnected during upload")
		}
		result := res.File
		if result == nil {
			return nil, fmt.Errorf("agent upload returned no result envelope")
		}
		s.broadcastSystemMessage(sessionID, SysFileUploaded,
			fmt.Sprintf("File uploaded: %s → %s", filename, result.Path),
			map[string]any{"filename": filename, "path": result.Path})
		return result, nil
	case <-ctx.Done():
		_ = s.agents.CancelTask(nodeID, taskID)
		return nil, ctx.Err()
	}
}

func (s *Service) DeleteSession(ctx context.Context, id string) error {
	s.closeRemoteSession(id)
	return s.store.DeleteSession(ctx, id)
}

func (s *Service) closeRemoteSession(sessionID string) {
	session, err := s.store.GetSession(context.Background(), sessionID)
	if err != nil || s.agents == nil || session.GetSession().GetNodeId() == "" {
		return
	}
	requestID := "close:" + sessionID
	_ = s.agents.sendAgentMessage(session.GetSession().GetNodeId(), requestID, "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CloseSessionRequest{CloseSessionRequest: &aop.CloseSessionRequest{
		SessionId: sessionID, Reason: "completed",
	}}})
}

// broadcastSystemMessage persists + broadcasts a system message. code names a
// translatable template rendered client-side via i18n (see the Sys* codes);
// fallback is the English text kept in Content for non-i18n consumers, logs and
// tests. params feeds i18n interpolation and is stored next to code so the
// message stays localizable after a reload.
