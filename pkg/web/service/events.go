package service

import (
	"context"
	"strconv"

	aop "github.com/chainreactors/aiscan/aop"
	types "github.com/chainreactors/aiscan/pkg/types"
	proto "google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
	"google.golang.org/protobuf/types/known/structpb"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func (s *Service) BroadcastAOPEvent(sessionID string, event *aop.Event) {
	if s == nil || s.hub == nil || sessionID == "" || event == nil || event.Payload == nil {
		return
	}
	if !s.prepareAOPEvent(sessionID, event) {
		return
	}
	var cursor int64
	if s.store != nil {
		storedCursor, _, err := s.store.AppendAOPEvent(context.Background(), sessionID, event)
		if err != nil {
			return
		}
		cursor = storedCursor
	}
	s.broadcastAOPEvent(sessionID, event, cursor)
}

// PublishUserMessage records the operator input in the durable AOP timeline.
// Node delivery remains the caller's RunTurn/Command request; this function
// does not create a second transport path.
func (s *Service) PublishUserMessage(sessionID, turnID string, message *aop.Message) {
	if message == nil || len(message.Content) == 0 {
		return
	}
	userMessage := proto.CloneOf(message)
	if userMessage.Id == "" {
		userMessage.Id = generateID()
	}
	userMessage.Role = "user"
	s.BroadcastAOPEvent(sessionID, &aop.Event{
		SessionId: sessionID,
		TurnId:    turnID,
		Emitter:   "aiscan.web",
		Payload:   &aop.Event_Message{Message: userMessage},
	})
}

func (s *Service) prepareAOPEvent(sessionID string, event *aop.Event) bool {
	if event.SessionId == "" {
		event.SessionId = sessionID
	}
	if event.Id == "" {
		event.Id = generateID()
	}
	if event.EmittedAt == nil {
		event.EmittedAt = timestamppb.Now()
	}
	sequenceKey := event.SessionId
	if event.Seq == 0 && s.store != nil {
		s.eventMu.Lock()
		_, initialized := s.sessionSeq[sequenceKey]
		s.eventMu.Unlock()
		if !initialized {
			if maximum, err := s.store.MaxAOPEventSeq(context.Background(), sequenceKey); err == nil {
				s.eventMu.Lock()
				if _, exists := s.sessionSeq[sequenceKey]; !exists {
					s.sessionSeq[sequenceKey] = maximum
				}
				s.eventMu.Unlock()
			}
		}
	}
	s.eventMu.Lock()
	defer s.eventMu.Unlock()
	if event.GetTurnEnded() != nil && event.TurnId != "" {
		terminalKey := sequenceKey + "\x00" + event.TurnId
		if s.endedTurns[terminalKey] {
			return false
		}
		s.endedTurns[terminalKey] = true
	}
	if event.Seq == 0 {
		s.sessionSeq[sequenceKey]++
		event.Seq = s.sessionSeq[sequenceKey]
	} else if event.Seq > s.sessionSeq[sequenceKey] {
		s.sessionSeq[sequenceKey] = event.Seq
	}
	return true
}

func (s *Service) resetTurnTerminal(sessionID, turnID string) {
	if sessionID == "" || turnID == "" {
		return
	}
	s.eventMu.Lock()
	delete(s.endedTurns, sessionID+"\x00"+turnID)
	s.eventMu.Unlock()
}

func (s *Service) broadcastAOPEvent(sessionID string, event *aop.Event, cursor int64) {
	deliveryCursor := ""
	if cursor > 0 {
		deliveryCursor = strconv.FormatInt(cursor, 10)
	}
	s.hub.BroadcastAOP(sessionID, &aop.EventDelivery{Cursor: deliveryCursor, Event: event}, isReliableAOPEvent(event))
}

// broadcastHubError emits a hub-originated failure as an AOP error event: the
// code names a translatable template (mirrored under `sys.*` in the frontend
// locales), message is the English fallback, and params feed i18n
// interpolation via the aiscan.web extension.
func (s *Service) broadcastHubError(sessionID, code, message string, params map[string]any) {
	event := &aop.Event{
		Id: generateID(), EmittedAt: timestamppb.Now(), SessionId: sessionID, Emitter: "aiscan.web",
		Payload: &aop.Event_Error{Error: &aop.ProtocolError{Code: code, Message: message}},
	}
	if len(params) > 0 {
		if values, err := structpb.NewStruct(params); err == nil {
			_ = types.SetWebMessage(event, &types.WebMessageMetadata{Params: values})
		}
	}
	s.BroadcastAOPEvent(sessionID, event)
}

func (s *Service) broadcastHubTurnEnded(sessionID, turnID, code, message string) {
	ended := &aop.TurnEnded{StopReason: "error", Error: &aop.ProtocolError{Code: code, Message: message}}
	s.BroadcastAOPEvent(sessionID, &aop.Event{
		SessionId: sessionID, TurnId: turnID, Emitter: "aiscan.web",
		Payload: &aop.Event_TurnEnded{TurnEnded: ended},
	})
}

func isReliableAOPEvent(event *aop.Event) bool {
	switch payload := event.Payload.(type) {
	case *aop.Event_SessionEnded, *aop.Event_Error, *aop.Event_ToolResult, *aop.Event_TurnEnded, *aop.Event_Message:
		return true
	case *aop.Event_Status:
		// Status entries that drive durable UI state (eval/compact banners,
		// budget warnings) must survive reconnect; the rest are evictable.
		switch payload.Status.State {
		case types.EvalStateEnd, types.CompactStateEnd, "token_budget_warning":
			return true
		}
	}
	return false
}

// runHubCommand executes a product-level slash command that needs hub state.
// name is the canonical catalog name without its leading slash. Agent-scope
// commands never reach here; they fall through to the agent bridge.
func (s *Service) broadcastSystemMessage(sessionID, code, fallback string, params map[string]any) {
	metadata := &types.WebMessageMetadata{Code: code}
	if code != "" {
		metadata.Params, _ = structpb.NewStruct(params)
	}
	s.broadcastSystemMessageMetadata(sessionID, fallback, metadata)
}

func (s *Service) broadcastSystemMessageMetadata(sessionID, fallback string, metadata *types.WebMessageMetadata) {
	event := &aop.Event{
		Id: generateID(), EmittedAt: timestamppb.Now(), SessionId: sessionID, Emitter: "aiscan.web",
		Payload: &aop.Event_Message{Message: &aop.Message{
			Id: generateID(), Role: "system", Content: []*aop.Content{aop.Text(fallback)},
		}},
	}
	if metadata != nil && (metadata.GetCode() != "" || metadata.GetNodeId() != "" || metadata.GetParams() != nil || metadata.GetAgentList() != nil) {
		_ = types.SetWebMessage(event, metadata)
	}
	s.BroadcastAOPEvent(sessionID, event)
}

func (s *Service) broadcastScanComplete(scanID string) {
	s.mu.Lock()
	sid, ok := s.taskSessions[scanID]
	s.mu.Unlock()
	if !ok {
		return
	}
	if s.finishSessionTask(scanID) {
		return
	}
	_ = s.store.LinkScanToSession(context.Background(), sid, scanID)
	value, err := anypb.New(&types.SessionScanEvent{ScanId: scanID, Status: types.ScanStatus_SCAN_STATUS_COMPLETED})
	if err != nil {
		return
	}
	s.BroadcastAOPEvent(sid, &aop.Event{
		SessionId: sid,
		Emitter:   "aiscan.web",
		Payload:   &aop.Event_Extension{Extension: value},
	})
}
