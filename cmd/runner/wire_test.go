package main

import (
	"bufio"
	"context"
	"errors"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	filepb "github.com/chainreactors/cyber/aop/file"
	operationpb "github.com/chainreactors/cyber/aop/operation"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/gorilla/websocket"
	"google.golang.org/protobuf/encoding/protojson"
)

func TestRunnerOverAOP(t *testing.T) {
	t.Run("files", func(t *testing.T) { runnerOverAOP(t, false) })
	t.Run("files_observe_skills", func(t *testing.T) { runnerOverAOP(t, true) })
}

func runnerOverAOP(t *testing.T, withExtensions bool) {
	workDir := t.TempDir()
	skillsDir := t.TempDir()
	outputPath := filepath.Join(t.TempDir(), "events.jsonl")
	if withExtensions {
		if err := os.WriteFile(filepath.Join(skillsDir, "SKILL.md"), []byte("local test instructions"), 0600); err != nil {
			t.Fatal(err)
		}
	}

	serverErr := make(chan error, 1)
	completed := make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, request *http.Request) {
		conn, err := (&websocket.Upgrader{CheckOrigin: func(*http.Request) bool { return true }}).Upgrade(w, request, nil)
		if err != nil {
			serverErr <- err
			return
		}
		defer conn.Close()
		_ = conn.SetReadDeadline(time.Now().Add(5 * time.Second))
		if err := exerciseFilesProfile(conn); err != nil {
			serverErr <- err
			return
		}
		if withExtensions {
			if err := sendToolCall(conn, "skill-1", "read", map[string]any{"path": "skill://SKILL.md"}); err != nil {
				serverErr <- err
				return
			}
			result, err := receiveToolResult(conn, "skill-1")
			if err != nil {
				serverErr <- err
				return
			}
			if result.GetIsError() || len(result.GetOutput()) != 1 || result.GetOutput()[0].GetText().GetText() != "local test instructions" {
				serverErr <- errors.New("mounted skill read failed")
				return
			}
		}
		close(completed)
	}))
	defer server.Close()

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	done := make(chan error, 1)
	go func() {
		args := []string{"--server", server.URL, "--ws-path", "/runner", "--id", "files-test-runner", "--workdir", workDir, "--json"}
		if withExtensions {
			args = append(args, "--extensions", "files,observe,skills", "--skills-dir", skillsDir, "--output", outputPath)
		}
		done <- run(ctx, args, io.Discard, io.Discard)
	}()

	select {
	case <-completed:
	case err := <-serverErr:
		cancel()
		t.Fatal(err)
	case err := <-done:
		cancel()
		t.Fatalf("tool node stopped before handshake: %v", err)
	case <-time.After(5 * time.Second):
		cancel()
		t.Fatal("timed out exercising file runner")
	}
	cancel()
	select {
	case err := <-done:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("tool node result: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("tool node did not stop")
	}
	data, err := os.ReadFile(filepath.Join(workDir, "result.txt"))
	if err != nil || string(data) != "cairn-profile" {
		t.Fatalf("written file = %q, %v", data, err)
	}
	if withExtensions {
		output, err := os.Open(outputPath)
		if err != nil {
			t.Fatal(err)
		}
		defer output.Close()
		scanner := bufio.NewScanner(output)
		var accesses []*filepb.Access
		var refs []*operationpb.Ref
		for scanner.Scan() {
			event := new(aop.Event)
			if err := protojson.Unmarshal(scanner.Bytes(), event); err != nil {
				t.Fatal(err)
			}
			access := new(filepb.Access)
			if event.GetExtension() == nil || !event.GetExtension().MessageIs(access) {
				continue
			}
			if err := event.GetExtension().UnmarshalTo(access); err != nil {
				t.Fatal(err)
			}
			ref := new(operationpb.Ref)
			if ok, err := aop.FindTypedExtension(event, ref); err != nil || !ok {
				t.Fatalf("file event has no operation: %v %v", event, err)
			}
			accesses, refs = append(accesses, access), append(refs, ref)
		}
		if err := scanner.Err(); err != nil {
			t.Fatal(err)
		}
		if len(accesses) != 2 || accesses[0].GetOp() != filepb.AccessOp_ACCESS_OP_CREATE || accesses[1].GetOp() != filepb.AccessOp_ACCESS_OP_READ || refs[0].GetCallId() != "write-1" || refs[1].GetCallId() != "read-1" {
			t.Fatalf("observation stream lost real file operations or included virtual reads: %v", accesses)
		}
	}
}

func exerciseFilesProfile(conn *websocket.Conn) error {
	helloEnvelope, message, err := receiveJSON(conn)
	if err != nil {
		return err
	}
	protocol, ok := message.(*aop.ProtocolMessage)
	if !ok || protocol.GetAgentHello() == nil {
		return errors.New("missing tool node hello")
	}
	hello := protocol.GetAgentHello()
	if len(hello.GetCapabilities()) != 1 || hello.GetCapabilities()[0] != "tool" {
		return errors.New("file profile advertised a non-tool capability")
	}
	names := map[string]bool{}
	for _, definition := range hello.GetTools() {
		names[definition.GetName()] = true
	}
	if !names["read"] || !names["write"] || !names["ls"] || !names["glob"] || len(names) != 4 {
		return errors.New("file profile advertised an unexpected tool registry")
	}
	accepted := aop.Reply(helloEnvelope.GetId(), &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentAccepted{
		AgentAccepted: &aop.AgentAccepted{NodeId: hello.GetNodeId()},
	}})
	if err := sendJSON(conn, accepted); err != nil {
		return err
	}
	if err := sendToolCall(conn, "write-1", "write", map[string]any{"path": "result.txt", "content": "cairn-profile"}); err != nil {
		return err
	}
	written, err := receiveToolResult(conn, "write-1")
	if err != nil {
		return err
	}
	if written.GetIsError() || written.GetCallId() != "write-1" || written.GetName() != "write" {
		return errors.New("write returned an invalid terminal result")
	}
	if err := sendToolCall(conn, "read-1", "read", map[string]any{"path": "result.txt"}); err != nil {
		return err
	}
	read, err := receiveToolResult(conn, "read-1")
	if err != nil {
		return err
	}
	if read.GetIsError() || read.GetCallId() != "read-1" || read.GetName() != "read" || len(read.GetOutput()) != 1 || read.GetOutput()[0].GetText().GetText() != "cairn-profile" {
		return errors.New("read returned an invalid terminal result")
	}
	return nil
}

func sendToolCall(conn *websocket.Conn, id, name string, arguments map[string]any) error {
	value, err := aop.JSONValue(arguments)
	if err != nil {
		return err
	}
	return sendJSON(conn, aop.MustWrap(id, "", &toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Call{
		Call: &toolpb.Call{Call: &aop.ToolCall{Id: id, Name: name, Arguments: value}},
	}}))
}

func receiveToolResult(conn *websocket.Conn, callID string) (*aop.ToolResult, error) {
	for {
		envelope, message, err := receiveJSON(conn)
		if err != nil {
			return nil, err
		}
		protocol, ok := message.(*aop.ProtocolMessage)
		if !ok || protocol.GetEvent() == nil {
			continue
		}
		event := protocol.GetEvent()
		if event.GetSessionStarted() != nil || event.GetTurnStarted() != nil {
			return nil, errors.New("tool profile emitted a synthetic Agent lifecycle event")
		}
		if result := event.GetToolResult(); result != nil {
			if envelope.GetReplyTo() != callID {
				return nil, errors.New("tool result reply correlation is invalid")
			}
			return result, nil
		}
	}
}

func sendJSON(conn *websocket.Conn, envelope *aop.Envelope) error {
	data, err := protojson.Marshal(envelope)
	if err != nil {
		return err
	}
	return conn.WriteMessage(websocket.TextMessage, data)
}

func receiveJSON(conn *websocket.Conn) (*aop.Envelope, any, error) {
	_, data, err := conn.ReadMessage()
	if err != nil {
		return nil, nil, err
	}
	envelope := new(aop.Envelope)
	if err := protojson.Unmarshal(data, envelope); err != nil {
		return nil, nil, err
	}
	message, err := aop.Unwrap(envelope)
	return envelope, message, err
}
