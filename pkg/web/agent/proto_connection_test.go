package agent

import (
	"context"
	execpb "github.com/chainreactors/aiscan/aop/exec"
	filepb "github.com/chainreactors/aiscan/aop/file"
	"github.com/chainreactors/aiscan/pkg/commands"
	protobuf "google.golang.org/protobuf/proto"
	"os"
	"path/filepath"
	"runtime"
	"testing"
)

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
