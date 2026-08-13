package node

import (
	"bytes"
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	execpb "github.com/chainreactors/aiscan/aop/exec"
	filepb "github.com/chainreactors/aiscan/aop/file"
	toolpb "github.com/chainreactors/aiscan/aop/tool"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
	types "github.com/chainreactors/aiscan/pkg/types"
	protobuf "google.golang.org/protobuf/proto"
)

type handshakeThenEOFStream struct {
	helloID string
	recvs   int
}

func (s *handshakeThenEOFStream) Send(envelope *aop.Envelope) error {
	if s.helloID == "" {
		s.helloID = envelope.GetId()
	}
	return nil
}

func (s *handshakeThenEOFStream) Recv() (*aop.Envelope, error) {
	s.recvs++
	if s.recvs == 1 {
		return aop.MustWrap("accepted", s.helloID, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentAccepted{AgentAccepted: &aop.AgentAccepted{NodeId: "runner-1"}}}), nil
	}
	return nil, io.EOF
}

func TestServeAgentConnectionSubscribesBeforePublishingMenu(t *testing.T) {
	stream := new(handshakeThenEOFStream)
	subscribed := false
	menuCalled := false
	cc := connectionConfig{
		Name:     "runner-1",
		NodeID:   "runner-1",
		Registry: commands.NewRegistry(),
		AgentSubscribe: func(func(*aop.Event)) func() {
			subscribed = true
			return func() {}
		},
		Menu: func() []*types.CommandSpec {
			menuCalled = true
			if !subscribed {
				t.Error("command catalog was published before event subscription")
			}
			return nil
		},
	}
	if err := serveAgentConnection(context.Background(), cc, telemetry.NopLogger(), stream); err != io.EOF {
		t.Fatalf("serveAgentConnection error = %v, want EOF", err)
	}
	if !menuCalled {
		t.Fatal("command catalog was not published")
	}
}

func TestToolOperationPanicIsReportedAndCleanedUp(t *testing.T) {
	var logs bytes.Buffer
	logger := telemetry.NewLogger(telemetry.LogConfig{Debug: true, Output: &logs})
	operations := make(map[string]context.CancelFunc)
	var operationsMu sync.Mutex
	failure := make(chan *aop.ProtocolError, 1)
	send := func(_ string, message protobuf.Message) {
		protocol := message.(*aop.ProtocolMessage)
		if protocol.GetEvent() != nil {
			panic("send event boom")
		}
		if value := protocol.GetProtocolError(); value != nil {
			failure <- value
		}
	}
	arguments, _ := aop.JSONValue(map[string]any{})
	request := &toolpb.Call{Call: &aop.ToolCall{Id: "op-panic", Name: "missing", Arguments: arguments}}
	handleAgentToolMessage(
		context.Background(),
		connectionConfig{Registry: commands.NewRegistry(), Logger: logger},
		&aop.Envelope{Id: "op-panic"},
		&toolpb.ProtocolMessage{Message: &toolpb.ProtocolMessage_Call{Call: request}},
		send, &operationsMu, operations, make(map[string]time.Time),
	)

	select {
	case got := <-failure:
		if got.Code != "OPERATION_FAILED" || !strings.Contains(got.Message, "unexpectedly") {
			t.Fatalf("failure = %+v", got)
		}
	case <-time.After(5 * time.Second):
		t.Fatal("timed out waiting for operation failure")
	}
	operationsMu.Lock()
	_, tracked := operations["op-panic"]
	operationsMu.Unlock()
	if tracked {
		t.Fatal("panicking operation was not cleaned up")
	}
	if got := logs.String(); !strings.Contains(got, "send event boom") || !strings.Contains(got, "op-panic") {
		t.Fatalf("panic log = %s", got)
	}
}

// Canceling a call does not stop a scanner that ignores its context, so the
// hub having given up must also close that call's artifact window — otherwise
// the rest of the crawl crosses the wire only to be rejected on arrival.
func TestCancelOperationSealsTheCallArtifactWindow(t *testing.T) {
	var operationsMu sync.Mutex
	operations := make(map[string]context.CancelFunc)
	sealed := make(map[string]time.Time)
	canceled := false
	operations["op-1"] = func() { canceled = true }

	handleAgentCoreMessage(
		context.Background(),
		connectionConfig{Registry: commands.NewRegistry(), Logger: telemetry.NopLogger()},
		&aop.Envelope{Id: "cancel-1"},
		&aop.ProtocolMessage{Message: &aop.ProtocolMessage_CancelOperation{CancelOperation: &aop.CancelOperation{TargetId: "op-1"}}},
		func(string, protobuf.Message) { t.Error("cancel must not answer on the wire") },
		func(*aop.Envelope) { t.Error("cancel must not reach the runtime") },
		&operationsMu, operations, sealed,
	)

	if !canceled {
		t.Fatal("cancel did not reach the operation")
	}
	if !callIsSealed(&operationsMu, sealed, "op-1") {
		t.Fatal("canceled call was left able to emit artifacts")
	}
	if callIsSealed(&operationsMu, sealed, "agent-loop-call") {
		t.Fatal("a call this connection never dispatched must not be sealed")
	}
}

func TestExecRequestCompletesWithOutput(t *testing.T) {
	command := "printf hello"
	if runtime.GOOS == "windows" {
		command = "echo|set /p=hello"
	}
	var messages []*execpb.ProtocolMessage
	handleExecRequest(context.Background(), &execpb.Request{Command: command, TimeoutSeconds: 5}, t.TempDir(), "exec-1", func(_ string, message protobuf.Message) {
		if value, ok := message.(*execpb.ProtocolMessage); ok {
			messages = append(messages, value)
		}
	})
	if len(messages) != 2 || string(messages[0].GetOutput().Data) != "hello" || messages[1].GetResult().State != "completed" {
		t.Fatalf("unexpected messages: %#v", messages)
	}
}

func TestExecRequestReportsExitCode(t *testing.T) {
	command := "exit 7"
	if runtime.GOOS == "windows" {
		command = "exit /b 7"
	}
	var result *execpb.Result
	handleExecRequest(context.Background(), &execpb.Request{Command: command, TimeoutSeconds: 5}, t.TempDir(), "exec-2", func(_ string, message protobuf.Message) {
		if value, ok := message.(*execpb.ProtocolMessage); ok && value.GetResult() != nil {
			result = value.GetResult()
		}
	})
	if result == nil || result.ExitCode != 7 {
		t.Fatalf("result = %+v, want exit code 7", result)
	}
}

func TestDefaultAgentRuntimeDoesNotAdvertiseRunnerFileRPCs(t *testing.T) {
	hello, err := BuildHello("agent", commands.NewRegistry(), "agent", nil)
	if err != nil {
		t.Fatal(err)
	}
	for _, capability := range hello.Capabilities {
		if capability == "file.list" || capability == "file.mkdir" {
			t.Fatalf("regular agent advertised runner-only capability %q", capability)
		}
	}
}

func TestFileListReturnsStructuredEntries(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "note.txt"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}
	value := fileList(&filepb.ListRequest{Path: "."}, base)
	if value.err != nil {
		t.Fatal(value.err)
	}
	if value.result.Path != "." || len(value.result.Entries) != 2 {
		t.Fatalf("result = %+v", value.result)
	}
	byName := map[string]*filepb.Entry{}
	for _, entry := range value.result.Entries {
		byName[entry.Name] = entry
	}
	if byName["note.txt"].IsDirectory || byName["note.txt"].Size != 4 {
		t.Fatalf("file entry = %+v", byName["note.txt"])
	}
	if !byName["nested"].IsDirectory {
		t.Fatalf("directory entry = %+v", byName["nested"])
	}
}

func TestNativeFileRPCsResolveRelativeToRuntimeWorkdir(t *testing.T) {
	base := t.TempDir()
	if value := fileMkdir(&filepb.MkdirRequest{Path: "nested"}, base); value.err != nil {
		t.Fatal(value.err)
	}
	path := filepath.Join("nested", "proof.txt")
	if value := fileWrite(&filepb.WriteRequest{Path: path, Data: []byte("hello")}, base); value.err != nil {
		t.Fatal(value.err)
	}
	value := fileRead(&filepb.ReadRequest{Path: path}, base)
	if value.err != nil || string(value.result.Data) != "hello" {
		t.Fatalf("read data = %q, err = %v", value.result.Data, value.err)
	}
}

func TestFileReadReturnsBoundedChunks(t *testing.T) {
	base := t.TempDir()
	path := filepath.Join(base, "capture.mp4")
	data := bytes.Repeat([]byte("frame"), 300_000)
	if err := os.WriteFile(path, data, 0o644); err != nil {
		t.Fatal(err)
	}
	first := fileRead(&filepb.ReadRequest{Path: path, Limit: 256 * 1024}, base)
	if first.err != nil {
		t.Fatal(first.err)
	}
	if first.result.Offset != 0 || first.result.Eof || len(first.result.Data) != 256*1024 || first.result.Size != int64(len(data)) {
		t.Fatalf("first chunk = %+v, bytes=%d", first.result, len(first.result.Data))
	}
	if first.result.MediaType != "video/mp4" {
		t.Fatalf("media type = %q, want video/mp4", first.result.MediaType)
	}
	joined := append([]byte(nil), first.result.Data...)
	offset := int64(len(joined))
	for {
		next := fileRead(&filepb.ReadRequest{Path: path, Offset: offset, Limit: maxFileReadChunkBytes + 1}, base)
		if next.err != nil {
			t.Fatal(next.err)
		}
		if next.result.Offset != offset || len(next.result.Data) > int(maxFileReadChunkBytes) {
			t.Fatalf("chunk offset=%d bytes=%d, want offset=%d max=%d", next.result.Offset, len(next.result.Data), offset, maxFileReadChunkBytes)
		}
		joined = append(joined, next.result.Data...)
		offset += int64(len(next.result.Data))
		if next.result.Eof {
			break
		}
	}
	if !bytes.Equal(joined, data) {
		t.Fatalf("joined bytes = %d, want %d", len(joined), len(data))
	}
}

func TestFileReadDoesNotDecodePathEncodedRanges(t *testing.T) {
	encoded := "aop-range://read?path=proof.txt&offset=1&limit=2"
	value := fileRead(&filepb.ReadRequest{Path: encoded}, t.TempDir())
	if value.err == nil {
		t.Fatal("path-encoded range unexpectedly succeeded")
	}
	if value.result.Path != encoded {
		t.Fatalf("result path = %q, want original path %q", value.result.Path, encoded)
	}
}

func TestUploadWritesAbsolutePath(t *testing.T) {
	const filename = "aiscan_test_upload_probe.txt"
	const body = "codex public proof\nkey=appImage/probe"
	dest := filepath.Join(os.TempDir(), "aiscan-uploads", filename)
	t.Cleanup(func() { _ = os.Remove(dest) })
	result, err := (&chatAgentHandler{}).Upload(&filepb.UploadRequest{SessionId: "sess-1", Filename: filename, Data: []byte(body)})
	if err != nil {
		t.Fatal(err)
	}
	if result.Path != dest {
		t.Fatalf("result = %+v, want path %q", result, dest)
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != body {
		t.Fatalf("file on disk = %q, err=%v; want %q", data, err, body)
	}
}

func TestShouldResetReconnectBackoff(t *testing.T) {
	connectedAt := time.Now()
	tests := []struct {
		name           string
		connectedAt    time.Time
		disconnectedAt time.Time
		want           bool
	}{
		{name: "dial failure", disconnectedAt: connectedAt},
		{name: "short session", connectedAt: connectedAt, disconnectedAt: connectedAt.Add(reconnectStableAfter - time.Second)},
		{name: "stable session", connectedAt: connectedAt, disconnectedAt: connectedAt.Add(reconnectStableAfter), want: true},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := shouldResetReconnectBackoff(tt.connectedAt, tt.disconnectedAt); got != tt.want {
				t.Fatalf("shouldResetReconnectBackoff() = %t, want %t", got, tt.want)
			}
		})
	}
}

type writeFailureStream struct {
	helloID   string
	accepted  bool
	closed    chan struct{}
	closeOnce sync.Once
	sends     atomic.Int32
	err       error
}

func (s *writeFailureStream) Send(envelope *aop.Envelope) error {
	if s.sends.Add(1) == 1 {
		s.helloID = envelope.GetId()
		return nil
	}
	return s.err
}

func (s *writeFailureStream) Recv() (*aop.Envelope, error) {
	if !s.accepted {
		s.accepted = true
		return aop.MustWrap("accepted", s.helloID, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentAccepted{AgentAccepted: &aop.AgentAccepted{NodeId: "runner-1"}}}), nil
	}
	<-s.closed
	return nil, io.ErrClosedPipe
}

func (s *writeFailureStream) Close() error {
	s.closeOnce.Do(func() { close(s.closed) })
	return nil
}

func TestServeAgentConnectionClosesStreamAfterWriteFailure(t *testing.T) {
	wantErr := errors.New("write failed")
	stream := &writeFailureStream{closed: make(chan struct{}), err: wantErr}
	done := make(chan error, 1)
	go func() {
		done <- serveAgentConnection(context.Background(), connectionConfig{
			Name:     "runner-1",
			NodeID:   "runner-1",
			Registry: commands.NewRegistry(),
			Menu:     func() []*types.CommandSpec { return nil },
		}, telemetry.NopLogger(), stream)
	}()

	select {
	case err := <-done:
		if !errors.Is(err, wantErr) {
			t.Fatalf("serveAgentConnection error = %v, want %v", err, wantErr)
		}
	case <-time.After(time.Second):
		t.Fatal("write failure did not unblock the receive loop")
	}
}
