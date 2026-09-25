package proc

import (
	"context"
	"time"
)

type FrameType string

const (
	FrameOpen     FrameType = "open"
	FrameOpened   FrameType = "opened"
	FrameAttach   FrameType = "attach"
	FrameAttached FrameType = "attached"
	FrameInput    FrameType = "input"
	FrameOutput   FrameType = "output"
	FrameResize   FrameType = "resize"
	FrameDetach   FrameType = "detach"
	FrameDetached FrameType = "detached"
	FrameKill     FrameType = "kill"
	FrameList     FrameType = "list"
	FrameSessions FrameType = "sessions"
	FrameClosed   FrameType = "closed"
	FrameError    FrameType = "error"
)

type Frame struct {
	Type      FrameType `json:"type"`
	StreamID  string    `json:"stream_id,omitempty"`
	SessionID string    `json:"session_id,omitempty"`
	Kind      string    `json:"kind,omitempty"`
	Name      string    `json:"name,omitempty"`
	Command   string    `json:"command,omitempty"`
	Args      []string  `json:"args,omitempty"`
	Data      []byte    `json:"data,omitempty"`
	Cols      int       `json:"cols,omitempty"`
	Rows      int       `json:"rows,omitempty"`
	Bytes     int       `json:"bytes,omitempty"`
	Offset    int64     `json:"offset,omitempty"`
	Singleton bool      `json:"singleton,omitempty"`
	Error     string    `json:"error,omitempty"`
	State     State     `json:"state,omitempty"`
	ExitCode  int       `json:"exit_code,omitempty"`
	Session   *Info     `json:"session,omitempty"`
	Sessions  []Info    `json:"sessions,omitempty"`
}

type OpenSpec struct {
	Kind    string
	Name    string
	Command string
	Args    []string
	Cols    int
	Rows    int
}

type ResizeFunc func(cols, rows int)

type OpenResult struct {
	Info   Info
	Resize ResizeFunc
}

type OpenFunc func(ctx context.Context, spec OpenSpec) (OpenResult, error)

type SessionManager interface {
	List() []Info
	Get(id string) (Info, bool)
	Write(id string, data []byte) error
	Resize(id string, cols, rows int) error
	Kill(id string) error
	SnapshotBytes(id string, n int) ([]byte, int64, error)
	MonitorFrom(ctx context.Context, id string, offset int64, interval time.Duration, push func([]byte)) error
	Wait(ctx context.Context, id string, timeout time.Duration) (Info, error)
}
