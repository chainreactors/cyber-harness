package record

import (
	"context"
	"image"
	"time"
)

const (
	defaultFPS         = 30
	maxSessionHistory  = 128
	sessionStopTimeout = 10 * time.Second

	sessionStarting  = "starting"
	sessionRecording = "recording"
	sessionStopping  = "stopping"
	sessionCompleted = "completed"
	sessionFailed    = "failed"
)

type Args struct {
	Action          string  `json:"action"                     jsonschema:"description=Operation to perform.,enum=screenshot,enum=record,enum=start,enum=stop,enum=status"`
	Target          string  `json:"target,omitempty"           jsonschema:"description=Capture target. Defaults to desktop.,enum=desktop,enum=window"`
	WindowHandle    string  `json:"window_handle,omitempty"    jsonschema:"description=Windows HWND or X11 window ID in decimal or 0x hexadecimal form"`
	PID             int64   `json:"pid,omitempty"              jsonschema:"description=Process ID used to resolve the largest visible top-level window"`
	DurationSeconds float64 `json:"duration_seconds,omitempty" jsonschema:"description=Duration for action=record; optional auto-stop duration for action=start"`
	FPS             int     `json:"fps,omitempty"              jsonschema:"description=Capture frame rate 1-60 (default 30),minimum=1,maximum=60"`
	Output          string  `json:"output,omitempty"           jsonschema:"description=Output path. Relative paths resolve against the invocation working directory"`
	RecordingID     string  `json:"recording_id,omitempty"     jsonschema:"description=Recording identifier required by stop and optional for status"`
}

type TargetInfo struct {
	Kind         string `json:"kind"`
	WindowHandle string `json:"window_handle,omitempty"`
	PID          int64  `json:"pid,omitempty"`
	Title        string `json:"title,omitempty"`
	Width        int    `json:"width,omitempty"`
	Height       int    `json:"height,omitempty"`
}

type SessionInfo struct {
	RecordingID string     `json:"recording_id"`
	State       string     `json:"state"`
	Target      TargetInfo `json:"target"`
	Output      string     `json:"output"`
	FPS         int        `json:"fps"`
	Frames      int64      `json:"frames,omitempty"`
	Bytes       int64      `json:"bytes,omitempty"`
	StartedAt   *time.Time `json:"started_at,omitempty"`
	EndedAt     *time.Time `json:"ended_at,omitempty"`
	DurationMS  int64      `json:"duration_ms,omitempty"`
	Error       string     `json:"error,omitempty"`
}

type captureRequest struct {
	Target       string
	WindowHandle uint64
	PID          int64
	FPS          int
}

type resolvedTarget struct {
	Info   TargetInfo
	Native any
}

type mediaInfo struct {
	Width  int
	Height int
	Frames int64
}

type captureBackend interface {
	Resolve(context.Context, captureRequest) (resolvedTarget, error)
	Screenshot(context.Context, resolvedTarget) (image.Image, error)
	Record(context.Context, resolvedTarget, string, int) (mediaInfo, error)
}
