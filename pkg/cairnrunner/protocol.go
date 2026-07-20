package cairnrunner

import (
	"encoding/json"
	"fmt"

	"github.com/chainreactors/utils/pty"
)

const chunkSize = 256 * 1024

type message struct {
	T       string          `json:"t,omitempty"`
	ID      uint32          `json:"id,omitempty"`
	Method  string          `json:"method,omitempty"`
	Params  json.RawMessage `json:"params,omitempty"`
	OK      bool            `json:"ok,omitempty"`
	Result  any             `json:"result,omitempty"`
	Error   string          `json:"error,omitempty"`
	Stream  string          `json:"stream,omitempty"`
	Data    string          `json:"data,omitempty"`
	Event   string          `json:"event,omitempty"`
	CallID  string          `json:"call_id,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`

	RunnerID string         `json:"runner_id,omitempty"`
	Hostname string         `json:"hostname,omitempty"`
	OS       string         `json:"os,omitempty"`
	Arch     string         `json:"arch,omitempty"`
	Meta     map[string]any `json:"meta,omitempty"`
	Version  string         `json:"version,omitempty"`
}

type execParams struct {
	Command string            `json:"command"`
	Cwd     string            `json:"cwd,omitempty"`
	Timeout int               `json:"timeout,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
}

type fileParams struct {
	Path string `json:"path"`
	Size int64  `json:"size,omitempty"`
}

type execResult struct {
	ExitCode  int     `json:"exitCode"`
	State     string  `json:"state,omitempty"`
	KillCause string  `json:"killCause,omitempty"`
	Duration  float64 `json:"duration,omitempty"`
}

func newPTYMessage(frame pty.Frame) message {
	payload, _ := json.Marshal(frame)
	return message{T: "pty", Payload: payload}
}

func decodePTYMessage(msg message) (pty.Frame, error) {
	var frame pty.Frame
	if msg.T != "pty" {
		return frame, fmt.Errorf("unsupported PTY envelope %q", msg.T)
	}
	if len(msg.Payload) == 0 {
		return frame, fmt.Errorf("PTY frame payload is required")
	}
	if err := json.Unmarshal(msg.Payload, &frame); err != nil {
		return frame, fmt.Errorf("decode PTY frame: %w", err)
	}
	if frame.Type == "" {
		return frame, fmt.Errorf("PTY frame type is required")
	}
	return frame, nil
}
