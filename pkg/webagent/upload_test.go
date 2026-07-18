package webagent

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/webproto"
)

// Pending upload notes must drain exactly once per session, stay scoped to their
// own session, and normalize the empty session ID to "default" (matching agentFor)
// so an upload and the chat turn that references it land in the same bucket.
func TestPendingUploadsDrainOncePerSession(t *testing.T) {
	m := newChatRuntimeManager(nil)

	m.notePendingUpload("s1", "note-a")
	m.notePendingUpload("s1", "note-b")
	m.notePendingUpload("s2", "note-c")

	got := m.takePendingUploads("s1")
	if got != "note-a\nnote-b" {
		t.Fatalf("s1 first drain = %q, want %q", got, "note-a\nnote-b")
	}
	if again := m.takePendingUploads("s1"); again != "" {
		t.Fatalf("s1 second drain = %q, want empty (drain is one-shot)", again)
	}
	if got := m.takePendingUploads("s2"); got != "note-c" {
		t.Fatalf("s2 drain = %q, want %q", got, "note-c")
	}

	// Empty session ID collapses to "default" on both sides.
	m.notePendingUpload("", "note-default")
	if got := m.takePendingUploads("default"); got != "note-default" {
		t.Fatalf("default drain = %q, want %q", got, "note-default")
	}
}

func TestPendingUploadsNilManagerSafe(t *testing.T) {
	var m *chatRuntimeManager
	m.notePendingUpload("s1", "note") // must not panic
	if got := m.takePendingUploads("s1"); got != "" {
		t.Fatalf("nil manager drain = %q, want empty", got)
	}
}

// handleFileUpload must write the bytes to the agent's local disk AND queue a note
// carrying that absolute path for the session's next turn — the fix for the LLM
// only ever seeing the hub's UI-only "file uploaded" notice and then guessing a
// bare filename against its cwd.
func TestHandleFileUploadRecordsAbsolutePathForNextTurn(t *testing.T) {
	m := newChatRuntimeManager(nil)

	const filename = "aiscan_test_upload_probe.txt"
	const body = "codex public proof\nkey=appImage/probe"
	dest := filepath.Join(os.TempDir(), "aiscan-uploads", filename)
	t.Cleanup(func() { _ = os.Remove(dest) })

	payload, _ := json.Marshal(webproto.FileUploadPayload{Filename: filename, SessionID: "sess-1"})
	msg := webproto.Message{
		Type:    "upload",
		TaskID:  "task-1",
		DataB64: base64.StdEncoding.EncodeToString([]byte(body)),
		Payload: payload,
	}

	var got webproto.Message
	handleFileUpload(msg, func(out webproto.Message) { got = out }, m)

	// The agent replied with the written path and no error.
	var res webproto.FileUploadResult
	if err := json.Unmarshal(got.Payload, &res); err != nil {
		t.Fatalf("decode result: %v", err)
	}
	if res.Error != "" {
		t.Fatalf("unexpected upload error: %s", res.Error)
	}
	if res.Path != dest {
		t.Fatalf("result path = %q, want %q", res.Path, dest)
	}

	// The bytes actually landed on disk.
	if data, err := os.ReadFile(dest); err != nil || string(data) != body {
		t.Fatalf("file on disk = %q, err=%v; want %q", data, err, body)
	}

	// The next turn for this session carries the absolute path so `read` resolves.
	note := m.takePendingUploads("sess-1")
	if !strings.Contains(note, dest) {
		t.Fatalf("pending note %q does not carry absolute path %q", note, dest)
	}
	if !strings.Contains(note, filename) {
		t.Fatalf("pending note %q does not name the file %q", note, filename)
	}
}
