package pty

import (
	"context"
	"testing"
	"time"

	"github.com/chainreactors/cyber/aop"
	ptypb "github.com/chainreactors/cyber/aop/pty"
	runtimeproc "github.com/chainreactors/utils/proc"
)

type recordingManager struct {
	info       runtimeproc.Info
	writes     [][]byte
	resizeCols int
	resizeRows int
	kills      int
	output     []byte
	monitors   []context.Context
}

func (m *recordingManager) List() []runtimeproc.Info { return []runtimeproc.Info{m.info} }
func (m *recordingManager) Get(id string) (runtimeproc.Info, bool) {
	return m.info, id == m.info.ID
}
func (m *recordingManager) Write(_ string, data []byte) error {
	m.writes = append(m.writes, append([]byte(nil), data...))
	return nil
}
func (m *recordingManager) Resize(_ string, cols, rows int) error {
	m.resizeCols, m.resizeRows = cols, rows
	return nil
}
func (m *recordingManager) Kill(_ string) error {
	m.kills++
	return nil
}
func (m *recordingManager) SnapshotBytes(_ string, _ int) ([]byte, int64, error) {
	return append([]byte(nil), m.output...), int64(len(m.output)), nil
}
func (m *recordingManager) MonitorFrom(ctx context.Context, _ string, _ int64, _ time.Duration, _ func([]byte)) error {
	m.monitors = append(m.monitors, ctx)
	return nil
}
func (m *recordingManager) Wait(ctx context.Context, _ string, _ time.Duration) (runtimeproc.Info, error) {
	<-ctx.Done()
	return runtimeproc.Info{}, ctx.Err()
}

func TestRouterHandlesCanonicalAOPMessages(t *testing.T) {
	manager := &recordingManager{
		info:   runtimeproc.Info{ID: "session-1", Kind: "repl", Name: "main-repl", State: runtimeproc.StateRunning},
		output: []byte("ready\n"),
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	messages := make(chan *ptypb.ProtocolMessage, 8)
	handler := newRouter(manager).Handler()

	dispatch(t, handler, ctx, &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Attach{Attach: &ptypb.Attach{
		StreamId: "stream-1", SessionId: "session-1", Cols: 120, Rows: 40,
	}}}, collect(messages))

	attached := readMessage(t, messages)
	if attached.GetAttached().GetSession().GetId() != "session-1" {
		t.Fatalf("attached = %+v", attached)
	}
	output := readMessage(t, messages)
	if string(output.GetOutput().GetData()) != "ready\n" {
		t.Fatalf("output = %+v", output)
	}
	if manager.resizeCols != 120 || manager.resizeRows != 40 {
		t.Fatalf("resize = %dx%d", manager.resizeCols, manager.resizeRows)
	}

	dispatch(t, handler, ctx, &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Input{Input: &ptypb.Input{
		StreamId: "stream-1", Data: []byte("/status\n"),
	}}}, collect(messages))
	if len(manager.writes) != 1 || string(manager.writes[0]) != "/status\n" {
		t.Fatalf("writes = %q", manager.writes)
	}
	dispatch(t, handler, ctx, &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Close{Close: &ptypb.Close{
		StreamId: "stream-1",
	}}}, collect(messages))
	if manager.kills != 1 {
		t.Fatalf("kills = %d", manager.kills)
	}
}

func TestRouterListAndDetachUseAOPResponses(t *testing.T) {
	manager := &recordingManager{info: runtimeproc.Info{ID: "session-1", State: runtimeproc.StateRunning}}
	messages := make(chan *ptypb.ProtocolMessage, 4)
	send := collect(messages)
	handler := newRouter(manager).Handler()

	dispatch(t, handler, context.Background(), ptypb.NewList("stream-1", "node-1"), send)
	sessions := readMessage(t, messages).GetSessions()
	if sessions.GetStreamId() != "stream-1" || len(sessions.GetSessions()) != 1 || sessions.GetSessions()[0].GetId() != "session-1" {
		t.Fatalf("sessions = %+v", sessions)
	}
	dispatch(t, handler, context.Background(), ptypb.NewDetach("stream-1"), send)
	if detached := readMessage(t, messages).GetDetached(); detached.GetStreamId() != "stream-1" {
		t.Fatalf("detached = %+v", detached)
	}
}

// One connection dropping must not reach into another: handlers created per
// connection keep their own stream table, and their monitors are children of
// the context that connection dispatches with.
func TestConnectionHandlersAndMonitorsAreIndependent(t *testing.T) {
	manager := &recordingManager{info: runtimeproc.Info{ID: "session-1", State: runtimeproc.StateRunning}}
	attached := func() *ptypb.ProtocolMessage {
		return &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Attach{Attach: &ptypb.Attach{
			StreamId: "stream-1", SessionId: "session-1",
		}}}
	}

	dropping, dropConnection := context.WithCancel(context.Background())
	defer dropConnection()
	surviving := make(chan *ptypb.ProtocolMessage, 4)
	dropped := make(chan *ptypb.ProtocolMessage, 4)
	first, second := newRouter(manager).Handler(), newRouter(manager).Handler()
	dispatch(t, first, dropping, attached(), collect(dropped))
	dispatch(t, second, context.Background(), attached(), collect(surviving))
	if len(manager.monitors) != 2 {
		t.Fatalf("monitors = %d, want one per connection", len(manager.monitors))
	}

	dropConnection()
	waitFor(t, func() bool { return manager.monitors[0].Err() != nil })
	if manager.monitors[1].Err() != nil {
		t.Fatalf("dropping one connection released the other connection's monitor")
	}

	dispatch(t, second, context.Background(), &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Input{Input: &ptypb.Input{
		StreamId: "stream-1", Data: []byte("/status\n"),
	}}}, collect(surviving))
	if len(manager.writes) != 1 {
		t.Fatalf("the surviving connection no longer routes its own stream: %q", manager.writes)
	}
}

func TestStreamAndNodeIdentityComeFromAOP(t *testing.T) {
	message := &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Open{Open: &ptypb.Open{
		StreamId: "stream-1", NodeId: "node-1",
	}}}
	if ptypb.StreamID(message) != "stream-1" || ptypb.NodeID(message) != "node-1" {
		t.Fatalf("identity = %q/%q", ptypb.StreamID(message), ptypb.NodeID(message))
	}
	if ptypb.StreamID(nil) != "" || ptypb.NodeID(nil) != "" {
		t.Fatal("nil protocol message must not have routing identity")
	}
}

// dispatch drives one namespace handler the way a connection-owned mux does:
// the request arrives wrapped in an Envelope and replies come back the same way.
func dispatch(t *testing.T, handler aop.NamespaceHandler, ctx context.Context, message *ptypb.ProtocolMessage, send SendFunc) {
	t.Helper()
	envelope := aop.MustWrap("envelope-1", "", message)
	if err := handler(ctx, envelope, message, func(out *aop.Envelope) error {
		reply := &ptypb.ProtocolMessage{}
		if err := out.GetPayload().UnmarshalTo(reply); err != nil {
			return err
		}
		send(reply)
		return nil
	}); err != nil {
		t.Fatalf("dispatch PTY message: %v", err)
	}
}

func collect(messages chan *ptypb.ProtocolMessage) SendFunc {
	return func(message *ptypb.ProtocolMessage) { messages <- message }
}

func readMessage(t *testing.T, messages <-chan *ptypb.ProtocolMessage) *ptypb.ProtocolMessage {
	t.Helper()
	select {
	case message := <-messages:
		if value := message.GetError(); value != nil {
			t.Fatalf("PTY error: %s", value.GetMessage())
		}
		return message
	case <-time.After(time.Second):
		t.Fatal("timed out waiting for PTY message")
		return nil
	}
}

func waitFor(t *testing.T, predicate func() bool) {
	t.Helper()
	deadline := time.Now().Add(time.Second)
	for !predicate() {
		if time.Now().After(deadline) {
			t.Fatal("condition not met within 1s")
		}
		time.Sleep(time.Millisecond)
	}
}

var _ runtimeproc.SessionManager = (*recordingManager)(nil)
