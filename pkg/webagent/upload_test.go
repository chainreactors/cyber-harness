package webagent

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/chainreactors/aiscan/pkg/webproto"
)

func TestHandleFileUploadWritesAbsolutePath(t *testing.T) {
	const filename = "aiscan_test_upload_probe.txt"
	const body = "codex public proof\nkey=appImage/probe"
	dest := filepath.Join(os.TempDir(), "aiscan-uploads", filename)
	t.Cleanup(func() { _ = os.Remove(dest) })

	payload, _ := json.Marshal(webproto.FileUploadPayload{Filename: filename, SessionID: "sess-1"})
	msg := webproto.Message{
		Type: "upload", TaskID: "task-1",
		DataB64: base64.StdEncoding.EncodeToString([]byte(body)), Payload: payload,
	}

	var got webproto.Message
	handleFileUpload(msg, func(out webproto.Message) { got = out })

	var result webproto.FileUploadResult
	if err := json.Unmarshal(got.Payload, &result); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if result.Error != "" || result.Path != dest {
		t.Fatalf("result = %+v, want path %q", result, dest)
	}
	if data, err := os.ReadFile(dest); err != nil || string(data) != body {
		t.Fatalf("file on disk = %q, err=%v; want %q", data, err, body)
	}
}
