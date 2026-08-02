package api

import (
	"context"
	"database/sql"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestRunTurnContinueSessionUsesRuntimeWithoutRepublishingInput(t *testing.T) {
	store := &sessionTestStore{session: &types.SessionRecord{
		Session:   &aop.Session{Id: "session-1", NodeId: "agent-1", State: SessionStateOpen},
		CreatedAt: timestamppb.Now(),
		UpdatedAt: timestamppb.Now(),
	}}
	runtime := &sessionTestRuntime{connected: true}
	sessions := NewSessions(store, runtime, func() string { return "generated" })

	response, err := sessions.RunTurn(context.Background(), "request-1", &aop.RunTurnRequest{
		SessionId:       "session-1",
		TurnId:          "turn-1",
		ContinueSession: true,
	})
	if err != nil {
		t.Fatal(err)
	}
	if response.GetAccepted().GetTurnId() != "turn-1" {
		t.Fatalf("RunTurn response = %v", response)
	}
	if runtime.started == nil || runtime.started.Input == nil {
		t.Fatal("runtime did not receive a normalized continuation request")
	}
	if runtime.published != 0 {
		t.Fatalf("continuation republished %d user messages", runtime.published)
	}
}

type sessionTestStore struct {
	session *types.SessionRecord
}

func (*sessionTestStore) LoadAOPRequest(context.Context, string, string, []byte, proto.Message) (bool, bool, error) {
	return false, false, nil
}

func (*sessionTestStore) SaveAOPRequest(context.Context, string, string, []byte, proto.Message) error {
	return nil
}

func (s *sessionTestStore) ListSessionPage(context.Context, int, int, bool) ([]*types.SessionRecord, bool, error) {
	if s.session == nil {
		return nil, false, nil
	}
	return []*types.SessionRecord{proto.Clone(s.session).(*types.SessionRecord)}, false, nil
}

func (s *sessionTestStore) GetSession(context.Context, string) (*types.SessionRecord, error) {
	if s.session == nil {
		return nil, sql.ErrNoRows
	}
	return proto.Clone(s.session).(*types.SessionRecord), nil
}

func (s *sessionTestStore) CreateSession(_ context.Context, session *types.SessionRecord) error {
	s.session = proto.Clone(session).(*types.SessionRecord)
	return nil
}

func (s *sessionTestStore) UpdateSession(_ context.Context, session *types.SessionRecord) error {
	s.session = proto.Clone(session).(*types.SessionRecord)
	return nil
}

func (s *sessionTestStore) DeleteSession(context.Context, string) error {
	s.session = nil
	return nil
}

func (*sessionTestStore) LinkScanToSession(context.Context, string, string) error { return nil }

func (*sessionTestStore) ListAOPEventsAfter(context.Context, string, int64, int) ([]*aop.EventDelivery, error) {
	return nil, nil
}

type sessionTestRuntime struct {
	connected bool
	started   *aop.RunTurnRequest
	published int
}

func (r *sessionTestRuntime) AgentInfo(string) (string, bool) { return "agent", r.connected }
func (*sessionTestRuntime) GetScan(context.Context, string) (*types.Scan, error) {
	return nil, sql.ErrNoRows
}
func (*sessionTestRuntime) OpenAgentSession(context.Context, string, *aop.OpenSessionRequest) error {
	return nil
}
func (*sessionTestRuntime) CloseAgentSession(context.Context, string, string, *aop.CloseSessionRequest) (bool, error) {
	return false, nil
}
func (r *sessionTestRuntime) StartAgentTurn(_ string, request *aop.RunTurnRequest) {
	r.started = proto.Clone(request).(*aop.RunTurnRequest)
}
func (*sessionTestRuntime) CancelTurn(context.Context, string, string) error  { return nil }
func (r *sessionTestRuntime) PublishUserMessage(string, string, *aop.Message) { r.published++ }
func (*sessionTestRuntime) BroadcastAOPEvent(string, *aop.Event)              {}
func (*sessionTestRuntime) SubscribeSessionEvents(string) (<-chan *aop.EventDelivery, func()) {
	ch := make(chan *aop.EventDelivery)
	return ch, func() { close(ch) }
}
func (*sessionTestRuntime) DeleteSession(context.Context, string) error { return nil }
func (*sessionTestRuntime) SessionMenu(string) []*types.CommandSpec     { return nil }
