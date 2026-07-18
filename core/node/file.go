package node

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"

	"github.com/chainreactors/aiscan/pkg/webproto"
)

// HandleFileRead reads a file from disk and sends its base64-encoded content.
func HandleFileRead(msg webproto.Message, send func(webproto.Message)) {
	var payload webproto.FileRPCPayload
	if len(msg.Payload) > 0 {
		_ = json.Unmarshal(msg.Payload, &payload)
	}
	payload.Path = strings.TrimSpace(payload.Path)
	if payload.Path == "" {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: "file path required"})
		return
	}

	data, err := os.ReadFile(payload.Path)
	if err != nil {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: err.Error()})
		return
	}
	payload.Size = int64(len(data))
	send(webproto.Message{
		Type:    "complete",
		TaskID:  msg.TaskID,
		DataB64: base64.StdEncoding.EncodeToString(data),
		Payload: webproto.MustJSON(payload),
	})
}

// HandleFileWrite writes base64-encoded data from a message to a file on disk.
func HandleFileWrite(msg webproto.Message, send func(webproto.Message)) {
	var payload webproto.FileRPCPayload
	if len(msg.Payload) > 0 {
		_ = json.Unmarshal(msg.Payload, &payload)
	}
	payload.Path = strings.TrimSpace(payload.Path)
	if payload.Path == "" {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: "file path required"})
		return
	}

	data, err := base64.StdEncoding.DecodeString(msg.DataB64)
	if err != nil {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: "decode file: " + err.Error()})
		return
	}
	if err := os.MkdirAll(filepath.Dir(payload.Path), 0o755); err != nil {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: err.Error()})
		return
	}
	if err := os.WriteFile(payload.Path, data, 0o644); err != nil {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: err.Error()})
		return
	}
	payload.Size = int64(len(data))
	send(webproto.Message{Type: "complete", TaskID: msg.TaskID, Payload: webproto.MustJSON(payload)})
}
