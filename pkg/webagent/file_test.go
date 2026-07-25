package webagent

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chainreactors/aiscan/pkg/webproto"
)

func captureFileRPC(t *testing.T, invoke func(func(webproto.Message))) webproto.Message {
	t.Helper()
	ch := make(chan webproto.Message, 1)
	invoke(func(msg webproto.Message) { ch <- msg })
	return <-ch
}

func TestDefaultAgentRuntimeDoesNotAdvertiseRunnerFileRPCs(t *testing.T) {
	for _, capability := range DefaultRuntime().Capabilities {
		if capability == "file.list" || capability == "file.mkdir" {
			t.Fatalf("regular agent advertised runner-only capability %q", capability)
		}
	}
}

func TestHandleFileListReturnsStructuredEntries(t *testing.T) {
	base := t.TempDir()
	if err := os.WriteFile(filepath.Join(base, "note.txt"), []byte("body"), 0o644); err != nil {
		t.Fatal(err)
	}
	if err := os.Mkdir(filepath.Join(base, "nested"), 0o755); err != nil {
		t.Fatal(err)
	}

	request := webproto.Message{
		Type:    "file.list",
		TaskID:  "list-1",
		Payload: webproto.MustJSON(webproto.FileRPCPayload{Path: "."}),
	}
	response := captureFileRPC(t, func(send func(webproto.Message)) {
		HandleFileList(request, base, send)
	})
	if response.Type != "complete" {
		t.Fatalf("response = %+v", response)
	}
	var result webproto.FileListResult
	if err := json.Unmarshal(response.Payload, &result); err != nil {
		t.Fatal(err)
	}
	if result.Path != "." || len(result.Entries) != 2 {
		t.Fatalf("result = %+v", result)
	}
	byName := map[string]webproto.FileEntry{}
	for _, entry := range result.Entries {
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

	mkdirRequest := webproto.Message{
		Type:    "file.mkdir",
		TaskID:  "mkdir-1",
		Payload: webproto.MustJSON(webproto.FileRPCPayload{Path: "nested"}),
	}
	response := captureFileRPC(t, func(send func(webproto.Message)) {
		HandleFileMkdir(mkdirRequest, base, send)
	})
	if response.Type != "complete" {
		t.Fatalf("mkdir response = %+v", response)
	}

	writeRequest := webproto.Message{
		Type:    "file.write",
		TaskID:  "write-1",
		DataB64: base64.StdEncoding.EncodeToString([]byte("hello")),
		Payload: webproto.MustJSON(webproto.FileRPCPayload{Path: filepath.Join("nested", "proof.txt")}),
	}
	response = captureFileRPC(t, func(send func(webproto.Message)) {
		HandleFileWrite(writeRequest, base, send)
	})
	if response.Type != "complete" {
		t.Fatalf("write response = %+v", response)
	}

	readRequest := webproto.Message{
		Type:    "file.read",
		TaskID:  "read-1",
		Payload: webproto.MustJSON(webproto.FileRPCPayload{Path: filepath.Join("nested", "proof.txt")}),
	}
	response = captureFileRPC(t, func(send func(webproto.Message)) {
		HandleFileRead(readRequest, base, send)
	})
	if response.Type != "complete" {
		t.Fatalf("read response = %+v", response)
	}
	data, err := base64.StdEncoding.DecodeString(response.DataB64)
	if err != nil || string(data) != "hello" {
		t.Fatalf("read data = %q, err = %v", data, err)
	}
}
