package runner

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"io"
	"strings"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"github.com/chainreactors/aiscan/core/telemetry"
	"google.golang.org/protobuf/encoding/protojson"
)

func newTestStdioHost(output io.Writer) *stdioHost {
	return newStdioHost(context.Background(), nil, telemetry.NopLogger(), output)
}

func protocolLine(t *testing.T, frame *transport.ServerFrame) string {
	t.Helper()
	data, err := protojson.Marshal(frame)
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func openSessionLine(t *testing.T, sessionID string) string {
	return protocolLine(t, &transport.ServerFrame{
		CorrelationId: "open-" + sessionID,
		Payload: &transport.ServerFrame_OpenSession{OpenSession: &aop.OpenSessionRequest{
			RequestId: "open-" + sessionID, SessionId: sessionID,
		}},
	})
}

func runLine(t *testing.T, sessionID, turnID, text string) string {
	return protocolLine(t, &transport.ServerFrame{
		CorrelationId: turnID,
		Payload: &transport.ServerFrame_RunTurn{RunTurn: &aop.RunTurnRequest{
			RequestId: turnID, SessionId: sessionID, TurnId: turnID,
			Input: &aop.Message{Id: "input-" + turnID, Role: "user", Content: []*aop.Content{aop.Text(text)}},
		}},
	})
}

func closeSessionLine(t *testing.T, sessionID, reason string) string {
	return protocolLine(t, &transport.ServerFrame{
		CorrelationId: "close-" + sessionID,
		Payload: &transport.ServerFrame_CloseSession{CloseSession: &aop.CloseSessionRequest{
			RequestId: "close-" + sessionID, SessionId: sessionID, Reason: reason,
		}},
	})
}

func TestStdioAcceptRejectsMalformedJSON(t *testing.T) {
	var output bytes.Buffer
	h := newTestStdioHost(&output)
	h.accept("not json")
	frames := decodeAgentFrames(t, &output)
	if len(frames) != 1 || frames[0].GetOperationError() == nil {
		t.Fatalf("frames = %#v", frames)
	}
	if !strings.Contains(frames[0].GetOperationError().Message, "decode frame") {
		t.Fatalf("error = %+v", frames[0].GetOperationError())
	}
}

func TestStdioAcceptRejectsUnsupportedFrame(t *testing.T) {
	var output bytes.Buffer
	h := newTestStdioHost(&output)
	h.accept(protocolLine(t, &transport.ServerFrame{CorrelationId: "future"}))
	frames := decodeAgentFrames(t, &output)
	if len(frames) != 1 || frames[0].GetOperationError() == nil || frames[0].CorrelationId != "future" {
		t.Fatalf("frames = %#v", frames)
	}
}

func TestStdioRunRequiresOpenSession(t *testing.T) {
	var output bytes.Buffer
	h := newRuntimeStdioHost(t, &output, nil)
	defer h.rt.Close()
	h.accept(runLine(t, "s1", "turn-1", "hello"))
	frames := decodeAgentFrames(t, &output)
	if len(frames) != 1 || frames[0].GetRunTurn().GetRejected() == nil {
		t.Fatalf("frames = %#v", frames)
	}
}

func TestStdioRunRejectsEmptyPrompt(t *testing.T) {
	var output bytes.Buffer
	h := newRuntimeStdioHost(t, &output, nil)
	defer h.rt.Close()
	h.accept(openSessionLine(t, "s1"))
	h.accept(runLine(t, "s1", "turn-1", "   "))
	h.drain()
	frames := decodeAgentFrames(t, &output)
	if frames[len(frames)-1].GetRunTurn().GetRejected() == nil {
		t.Fatalf("frames = %#v", frames)
	}
}

func TestStdioCommandUsesIndependentCorrelationID(t *testing.T) {
	var output bytes.Buffer
	h := newRuntimeStdioHost(t, &output, nil)
	defer h.rt.Close()
	h.accept(openSessionLine(t, "s1"))
	h.accept(protocolLine(t, &transport.ServerFrame{
		CorrelationId: "command-correlation",
		Payload: &transport.ServerFrame_Command{Command: &transport.CommandRequest{
			TaskId: "command-1", SessionId: "s1", Line: "/help",
		}},
	}))
	h.drain()
	for _, frame := range decodeAgentFrames(t, &output) {
		if frame.GetCommandResult() == nil {
			continue
		}
		if frame.CorrelationId != "command-correlation" || frame.GetCommandResult().TaskId != "command-1" {
			t.Fatalf("command result correlation = %+v", frame)
		}
		return
	}
	t.Fatal("command result missing")
}

func TestStdioHostReportsEncoderFailure(t *testing.T) {
	h := newTestStdioHost(failingWriter{})
	h.emitError("", errors.New("broken"))
	if err := h.err(); err == nil || !strings.Contains(err.Error(), "write stdio protocol") {
		t.Fatalf("host err = %v", err)
	}
}

func TestStdioDrainWithoutRuns(t *testing.T) {
	var output bytes.Buffer
	newTestStdioHost(&output).drain()
}

func decodeAgentFrames(t *testing.T, input *bytes.Buffer) []*transport.AgentFrame {
	t.Helper()
	var frames []*transport.AgentFrame
	scanner := bufio.NewScanner(bytes.NewReader(input.Bytes()))
	for scanner.Scan() {
		frame := new(transport.AgentFrame)
		if err := protojson.Unmarshal(scanner.Bytes(), frame); err != nil {
			t.Fatal(err)
		}
		frames = append(frames, frame)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return frames
}

func decodeAOPMessages(frames []*transport.AgentFrame) []*aop.Event {
	var events []*aop.Event
	for _, frame := range frames {
		if event := frame.GetEvent(); event != nil {
			events = append(events, event)
		}
	}
	return events
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }
