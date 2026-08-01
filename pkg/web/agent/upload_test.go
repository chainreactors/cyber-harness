package agent

import (
	"os"
	"path/filepath"
	"testing"

	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
)

func TestUploadWritesAbsolutePath(t *testing.T) {
	const filename = "aiscan_test_upload_probe.txt"
	const body = "codex public proof\nkey=appImage/probe"
	dest := filepath.Join(os.TempDir(), "aiscan-uploads", filename)
	t.Cleanup(func() { _ = os.Remove(dest) })
	result, err := (&chatAgentHandler{}).Upload(&transport.FileUploadRequest{TaskId: "task-1", SessionId: "sess-1", Filename: filename, Data: []byte(body)})
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
