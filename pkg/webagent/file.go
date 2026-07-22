package webagent

import (
	"encoding/base64"
	"encoding/json"
	"os"
	"path/filepath"

	"github.com/chainreactors/aiscan/pkg/webproto"
)

func resolveFileRPCPath(baseDir, path string) string {
	if filepath.IsAbs(path) || baseDir == "" {
		return filepath.Clean(path)
	}
	return filepath.Clean(filepath.Join(baseDir, path))
}

// HandleFileRead reads a file from disk and sends its base64-encoded content.
func HandleFileRead(msg webproto.Message, baseDir string, send func(webproto.Message)) {
	var payload webproto.FileRPCPayload
	if len(msg.Payload) > 0 {
		_ = json.Unmarshal(msg.Payload, &payload)
	}
	if payload.Path == "" {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: "file path required"})
		return
	}

	data, err := os.ReadFile(resolveFileRPCPath(baseDir, payload.Path))
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
func HandleFileWrite(msg webproto.Message, baseDir string, send func(webproto.Message)) {
	var payload webproto.FileRPCPayload
	if len(msg.Payload) > 0 {
		_ = json.Unmarshal(msg.Payload, &payload)
	}
	if payload.Path == "" {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: "file path required"})
		return
	}

	data, err := base64.StdEncoding.DecodeString(msg.DataB64)
	if err != nil {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: "decode file: " + err.Error()})
		return
	}
	resolved := resolveFileRPCPath(baseDir, payload.Path)
	if err := os.MkdirAll(filepath.Dir(resolved), 0o755); err != nil {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: err.Error()})
		return
	}
	if err := os.WriteFile(resolved, data, 0o644); err != nil {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: err.Error()})
		return
	}
	payload.Size = int64(len(data))
	send(webproto.Message{Type: "complete", TaskID: msg.TaskID, Payload: webproto.MustJSON(payload)})
}

// HandleFileList returns a directory listing as structured JSON. It does not
// invoke BashTool or parse terminal output.
func HandleFileList(msg webproto.Message, baseDir string, send func(webproto.Message)) {
	var payload webproto.FileRPCPayload
	if len(msg.Payload) > 0 {
		_ = json.Unmarshal(msg.Payload, &payload)
	}
	if payload.Path == "" {
		payload.Path = "."
	}

	entries, err := os.ReadDir(resolveFileRPCPath(baseDir, payload.Path))
	if err != nil {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: err.Error()})
		return
	}
	result := webproto.FileListResult{Path: payload.Path, Entries: make([]webproto.FileEntry, 0, len(entries))}
	for _, entry := range entries {
		info, err := entry.Info()
		if err != nil {
			send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: err.Error()})
			return
		}
		result.Entries = append(result.Entries, webproto.FileEntry{
			Name:        entry.Name(),
			IsDirectory: entry.IsDir(),
			Size:        info.Size(),
		})
	}
	send(webproto.Message{Type: "complete", TaskID: msg.TaskID, Payload: webproto.MustJSON(result)})
}

// HandleFileMkdir creates a directory using the host filesystem API.
func HandleFileMkdir(msg webproto.Message, baseDir string, send func(webproto.Message)) {
	var payload webproto.FileRPCPayload
	if len(msg.Payload) > 0 {
		_ = json.Unmarshal(msg.Payload, &payload)
	}
	if payload.Path == "" {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: "directory path required"})
		return
	}
	if err := os.MkdirAll(resolveFileRPCPath(baseDir, payload.Path), 0o755); err != nil {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: err.Error()})
		return
	}
	send(webproto.Message{Type: "complete", TaskID: msg.TaskID, Payload: webproto.MustJSON(payload)})
}
