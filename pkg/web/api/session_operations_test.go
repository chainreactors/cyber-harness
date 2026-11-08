package api

import (
	"bytes"
	"context"
	"database/sql"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	types "github.com/chainreactors/cyber/core/types"
	"google.golang.org/protobuf/proto"
)

type operationRecord struct {
	method         string
	hash, response []byte
}

type concurrentSessionStore struct {
	sessionTestStore
	mu       sync.Mutex
	sessions map[string]*types.SessionRecord
	requests map[string]operationRecord
}

func newConcurrentSessionStore() *concurrentSessionStore {
	return &concurrentSessionStore{sessions: map[string]*types.SessionRecord{}, requests: map[string]operationRecord{}}
}

func (s *concurrentSessionStore) GetSession(_ context.Context, id string) (*types.SessionRecord, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if record := s.sessions[id]; record != nil {
		return proto.CloneOf(record), nil
	}
	return nil, sql.ErrNoRows
}
func (s *concurrentSessionStore) CreateSession(_ context.Context, record *types.SessionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.sessions[record.Session.Id] != nil {
		return errors.New("session exists")
	}
	s.sessions[record.Session.Id] = proto.CloneOf(record)
	return nil
}
func (s *concurrentSessionStore) UpdateSession(_ context.Context, record *types.SessionRecord) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessions[record.Session.Id] = proto.CloneOf(record)
	return nil
}
func (s *concurrentSessionStore) DeleteSession(_ context.Context, id string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	delete(s.sessions, id)
	return nil
}
func (s *concurrentSessionStore) LoadAOPRequest(_ context.Context, id, method string, hash []byte, reply proto.Message) (bool, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	record, found := s.requests[id]
	if !found {
		return false, false, nil
	}
	if record.method != method || !bytes.Equal(record.hash, hash) {
		return false, true, nil
	}
	return true, false, proto.Unmarshal(record.response, reply)
}
func (s *concurrentSessionStore) SaveAOPRequest(_ context.Context, id, method string, hash []byte, reply proto.Message) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	raw, err := proto.Marshal(reply)
	if err != nil {
		return err
	}
	s.requests[id] = operationRecord{method, bytes.Clone(hash), raw}
	return nil
}

type heldOpenRuntime struct {
	sessionTestRuntime
	target                 string
	entered, release       chan struct{}
	openCalls, cancelCalls atomic.Int32
	once                   sync.Once
}

func (r *heldOpenRuntime) OpenAgentSession(ctx context.Context, _ string, request *aop.OpenSessionRequest) error {
	r.openCalls.Add(1)
	if request.SessionId != r.target {
		return nil
	}
	r.once.Do(func() { close(r.entered) })
	select {
	case <-r.release:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}
func (r *heldOpenRuntime) CancelTurn(context.Context, string, string) error {
	r.cancelCalls.Add(1)
	return nil
}
func (r *heldOpenRuntime) CloseAgentSession(context.Context, string, string, *aop.CloseSessionRequest) (bool, error) {
	return true, nil
}

func TestSlowSessionAdmissionDoesNotBlockOtherSessionCancellationOrMetadata(t *testing.T) {
	store := newConcurrentSessionStore()
	_ = store.CreateSession(t.Context(), &types.SessionRecord{Session: &aop.Session{Id: "b", NodeId: "node", State: SessionStateOpen}})
	runtime := &heldOpenRuntime{sessionTestRuntime: sessionTestRuntime{connected: true}, target: "a", entered: make(chan struct{}), release: make(chan struct{})}
	defer close(runtime.release)
	sessions := NewSessions(store, runtime, func() string { return "generated" })
	opened := make(chan error, 1)
	go func() {
		_, err := sessions.OpenSession(t.Context(), "held", &aop.OpenSessionRequest{SessionId: "a", NodeId: "node"})
		opened <- err
	}()
	<-runtime.entered
	finished := make(chan error, 1)
	go func() {
		response, err := sessions.CancelTurn(t.Context(), "cancel-b", &aop.CancelTurnRequest{SessionId: "b", TurnId: "turn-b"})
		if err == nil && response.GetAccepted() == nil {
			err = errors.New("cancel was rejected")
		}
		if err == nil {
			_, err = sessions.UpdateSession(t.Context(), &types.UpdateSessionRequest{RequestId: "rename-b", SessionId: "b", Title: proto.String("renamed"), Archived: proto.Bool(true)})
		}
		finished <- err
	}()
	select {
	case err := <-finished:
		if err != nil {
			t.Fatal(err)
		}
	case <-time.After(time.Second):
		t.Fatal("unrelated session waited for remote admission")
	}
	if runtime.cancelCalls.Load() != 1 {
		t.Fatal("cancel was not dispatched")
	}
	record, err := store.GetSession(t.Context(), "b")
	if err != nil || record.Session.Title != "renamed" || !record.Archived || record.Session.State != SessionStateOpen {
		t.Fatalf("metadata update = %v, %v", record, err)
	}
	select {
	case <-opened:
		t.Fatal("held admission unexpectedly finished")
	default:
	}
}

func TestDuplicateOperationWaitCanCancelWithoutInterruptingOwnerAndReplaysOnce(t *testing.T) {
	store := newConcurrentSessionStore()
	runtime := &heldOpenRuntime{sessionTestRuntime: sessionTestRuntime{connected: true}, target: "a", entered: make(chan struct{}), release: make(chan struct{})}
	var stop sync.Once
	defer stop.Do(func() { close(runtime.release) })
	sessions := NewSessions(store, runtime, func() string { return "generated" })
	request := &aop.OpenSessionRequest{SessionId: "a", NodeId: "node"}
	owner := make(chan *aop.OpenSessionResponse, 1)
	go func() { response, _ := sessions.OpenSession(t.Context(), "same-id", request); owner <- response }()
	<-runtime.entered
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := sessions.OpenSession(ctx, "same-id", proto.CloneOf(request)); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("duplicate wait = %v", err)
	}
	stop.Do(func() { close(runtime.release) })
	first := <-owner
	replayed, err := sessions.OpenSession(t.Context(), "same-id", proto.CloneOf(request))
	if err != nil || first.GetAccepted() == nil || !proto.Equal(first, replayed) || runtime.openCalls.Load() != 1 {
		t.Fatalf("replay=%v owner=%v calls=%d error=%v", replayed, first, runtime.openCalls.Load(), err)
	}
	conflict, err := sessions.CancelTurn(t.Context(), "same-id", &aop.CancelTurnRequest{SessionId: "a", TurnId: "turn"})
	if err != nil || conflict.GetRejected().GetCode() != "ALREADY_EXISTS" || runtime.cancelCalls.Load() != 0 {
		t.Fatalf("cross-method conflict=%v error=%v", conflict, err)
	}
	sessions.operationsMu.Lock()
	defer sessions.operationsMu.Unlock()
	if len(sessions.operations) != 0 {
		t.Fatal("completed and canceled operation gates were retained")
	}
}

func TestNestedResetChildIDCannotDeadlockAnotherReset(t *testing.T) {
	store := newConcurrentSessionStore()
	_ = store.CreateSession(t.Context(), &types.SessionRecord{Session: &aop.Session{Id: "old", NodeId: "node", State: SessionStateOpen}})
	runtime := &heldOpenRuntime{sessionTestRuntime: sessionTestRuntime{connected: true}, target: "new", entered: make(chan struct{}), release: make(chan struct{})}
	var stop sync.Once
	defer stop.Do(func() { close(runtime.release) })
	sessions := NewSessions(store, runtime, func() string { return "generated" })
	parent := make(chan *types.ResetSessionResponse, 1)
	go func() {
		response, _ := sessions.ResetSession(t.Context(), &types.ResetSessionRequest{RequestId: "reset", SessionId: "old", NewSessionId: "new"})
		parent <- response
	}()
	<-runtime.entered
	other := make(chan *types.ResetSessionResponse, 1)
	go func() {
		response, _ := sessions.ResetSession(t.Context(), &types.ResetSessionRequest{RequestId: "reset:open", SessionId: "old", NewSessionId: "third"})
		other <- response
	}()
	stop.Do(func() { close(runtime.release) })
	select {
	case response := <-parent:
		if response.GetAccepted() == nil {
			t.Fatalf("parent reset = %v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("parent reset deadlocked with its child request ID")
	}
	select {
	case response := <-other:
		if response.GetRejected().GetCode() != "ALREADY_EXISTS" {
			t.Fatalf("child conflict = %v", response)
		}
	case <-time.After(time.Second):
		t.Fatal("second reset never observed child ID conflict")
	}
}

func TestSessionOperationScopeUsesTheCanonicalSessionID(t *testing.T) {
	store := newConcurrentSessionStore()
	runtime := &heldOpenRuntime{sessionTestRuntime: sessionTestRuntime{connected: true}, target: "a", entered: make(chan struct{}), release: make(chan struct{})}
	defer close(runtime.release)
	sessions := NewSessions(store, runtime, func() string { return "generated" })
	go func() {
		_, _ = sessions.OpenSession(t.Context(), "owner", &aop.OpenSessionRequest{SessionId: " a ", NodeId: "node"})
	}()
	select {
	case <-runtime.entered:
	case <-time.After(time.Second):
		t.Fatal("owner admission did not start")
	}
	ctx, cancel := context.WithTimeout(t.Context(), 30*time.Millisecond)
	defer cancel()
	if _, err := sessions.OpenSession(ctx, "other", &aop.OpenSessionRequest{SessionId: "a", NodeId: "node"}); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("same-session wait = %v", err)
	}
	if runtime.openCalls.Load() != 1 {
		t.Fatal("whitespace bypassed same-session serialization")
	}
}

type turnReplayStore struct {
	*sessionTestStore
	events []*aop.EventDelivery
}

func (s *turnReplayStore) ListAOPEventsAfter(context.Context, string, int64, int) ([]*aop.EventDelivery, int64, error) {
	return s.events, 0, nil
}

func TestLegacyStoreTerminalLookupMatchesOriginAndExecutionGeneration(t *testing.T) {
	rootEnd := &aop.EventDelivery{Event: &aop.Event{SessionId: "root", TurnId: "turn", Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{}}}}
	childEnd := &aop.EventDelivery{Event: &aop.Event{SessionId: "child", TurnId: "turn", Payload: &aop.Event_TurnEnded{TurnEnded: &aop.TurnEnded{}}}}
	rootStart := &aop.EventDelivery{Event: &aop.Event{SessionId: "root", TurnId: "turn", Payload: &aop.Event_TurnStarted{TurnStarted: &aop.TurnStarted{}}}}
	for _, scenario := range []struct {
		name   string
		events []*aop.EventDelivery
		ended  bool
	}{
		{"root completed", []*aop.EventDelivery{rootEnd}, true},
		{"child completed only", []*aop.EventDelivery{childEnd}, false},
		{"new generation running", []*aop.EventDelivery{rootEnd, rootStart, childEnd}, false},
		{"new generation completed", []*aop.EventDelivery{rootEnd, rootStart, childEnd, rootEnd}, true},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			store := &turnReplayStore{sessionTestStore: &sessionTestStore{}, events: scenario.events}
			sessions := NewSessions(store, nil, nil)
			ended, err := sessions.turnEnded(t.Context(), "root", "turn")
			if err != nil || ended != scenario.ended {
				t.Fatalf("ended=%v error=%v", ended, err)
			}
		})
	}
}
