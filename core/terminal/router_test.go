package terminal

import (
	"context"
	"testing"
	"time"

	ptypb "github.com/chainreactors/aiscan/aop/pty"
	runtimepty "github.com/chainreactors/utils/pty"
)

type recordingManager struct {
	info       runtimepty.Info
	writes     [][]byte
	resizeCols int
	resizeRows int
	kills      int
	output     []byte
}

func (m *recordingManager) List() []runtimepty.Info { return []runtimepty.Info{m.info} }
func (m *recordingManager) Get(id string) (runtimepty.Info, bool) {
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
func (m *recordingManager) MonitorFrom(context.Context, string, int64, time.Duration, func([]byte)) error {
	return nil
}
func (m *recordingManager) Wait(ctx context.Context, _ string, _ time.Duration) (runtimepty.Info, error) {
	<-ctx.Done()
	return runtimepty.Info{}, ctx.Err()
}

func TestRouterHandlesCanonicalAOPMessages(t *testing.T) {
	manager := &recordingManager{
		info:   runtimepty.Info{ID: "session-1", Kind: "repl", Name: "main-repl", State: runtimepty.StateRunning},
		output: []byte("ready\n"),
	}
	router := NewRouter(manager)
	defer router.Close()
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	messages := make(chan *ptypb.ProtocolMessage, 8)
	send := func(message *ptypb.ProtocolMessage) { messages <- message }
	router.Handle(ctx, &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Attach{Attach: &ptypb.Attach{
		StreamId: "stream-1", SessionId: "session-1", Cols: 120, Rows: 40,
	}}}, send)

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

	router.Handle(ctx, &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Input{Input: &ptypb.Input{
		StreamId: "stream-1", Data: []byte("/status\n"),
	}}}, send)
	if len(manager.writes) != 1 || string(manager.writes[0]) != "/status\n" {
		t.Fatalf("writes = %q", manager.writes)
	}
	router.Handle(ctx, &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Close{Close: &ptypb.Close{
		StreamId: "stream-1",
	}}}, send)
	if manager.kills != 1 {
		t.Fatalf("kills = %d", manager.kills)
	}
}

func TestRouterListAndDetachUseAOPResponses(t *testing.T) {
	manager := &recordingManager{info: runtimepty.Info{ID: "session-1", State: runtimepty.StateRunning}}
	router := NewRouter(manager)
	defer router.Close()
	messages := make(chan *ptypb.ProtocolMessage, 4)
	send := func(message *ptypb.ProtocolMessage) { messages <- message }

	router.Handle(context.Background(), NewList("stream-1", "node-1"), send)
	sessions := readMessage(t, messages).GetSessions()
	if sessions.GetStreamId() != "stream-1" || len(sessions.GetSessions()) != 1 || sessions.GetSessions()[0].GetId() != "session-1" {
		t.Fatalf("sessions = %+v", sessions)
	}
	router.Handle(context.Background(), NewDetach("stream-1"), send)
	if detached := readMessage(t, messages).GetDetached(); detached.GetStreamId() != "stream-1" {
		t.Fatalf("detached = %+v", detached)
	}
}

func TestStreamAndNodeIdentityComeFromAOP(t *testing.T) {
	message := &ptypb.ProtocolMessage{Message: &ptypb.ProtocolMessage_Open{Open: &ptypb.Open{
		StreamId: "stream-1", NodeId: "node-1",
	}}}
	if StreamID(message) != "stream-1" || NodeID(message) != "node-1" {
		t.Fatalf("identity = %q/%q", StreamID(message), NodeID(message))
	}
	if StreamID(nil) != "" || NodeID(nil) != "" {
		t.Fatal("nil protocol message must not have routing identity")
	}
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

var _ runtimepty.SessionManager = (*recordingManager)(nil)
