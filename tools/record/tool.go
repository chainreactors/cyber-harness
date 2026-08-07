package record

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/imageutil"
)

type Tool struct {
	workDir       string
	outputDir     string
	maxConcurrent int
	backend       captureBackend

	mu       sync.RWMutex
	sessions map[string]*recordingSession
	closed   bool
}

func New(workDir, outputDir string, maxConcurrent int, backend captureBackend) *Tool {
	if maxConcurrent <= 0 {
		maxConcurrent = defaultMaxConcurrent
	}
	if maxConcurrent > maxConcurrentLimit {
		maxConcurrent = maxConcurrentLimit
	}
	return &Tool{
		workDir:       workDir,
		outputDir:     outputDir,
		maxConcurrent: maxConcurrent,
		backend:       backend,
		sessions:      make(map[string]*recordingSession),
	}
}

func (t *Tool) Name() string { return "record" }

func (t *Tool) Description() string {
	return "Capture desktop or application-window screenshots and H.264 MP4 recordings. Supports synchronous duration recording and asynchronous start/stop/status sessions."
}

func (t *Tool) Definition() *tool.Definition {
	return tool.Def(t.Name(), t.Description(), Args{})
}

func (t *Tool) Execute(ctx context.Context, arguments string) (*tool.Result, error) {
	args, err := tool.ParseArgs[Args](arguments)
	if err != nil {
		return nil, err
	}
	action := strings.ToLower(strings.TrimSpace(args.Action))
	if action == "" {
		return nil, fmt.Errorf("action is required")
	}
	if action != "status" && t.isClosed() {
		return nil, fmt.Errorf("record tool is closed")
	}

	switch action {
	case "screenshot":
		return t.screenshot(ctx, args)
	case "record":
		duration, err := normalizeDuration(args.DurationSeconds, true)
		if err != nil {
			return nil, err
		}
		session, err := t.start(ctx, args, duration)
		if err != nil {
			return nil, err
		}
		return t.waitResult(ctx, session)
	case "start":
		duration, err := normalizeDuration(args.DurationSeconds, false)
		if err != nil {
			return nil, err
		}
		session, err := t.start(ctx, args, duration)
		if err != nil {
			return nil, err
		}
		return jsonResult(session.snapshot())
	case "stop":
		return t.stop(ctx, strings.TrimSpace(args.RecordingID))
	case "status":
		return t.status(strings.TrimSpace(args.RecordingID))
	default:
		return nil, fmt.Errorf("unsupported action %q", args.Action)
	}
}

func (t *Tool) screenshot(ctx context.Context, args Args) (*tool.Result, error) {
	if t.backend == nil {
		return nil, fmt.Errorf("capture backend is unavailable")
	}
	req, err := normalizeCaptureArgs(args)
	if err != nil {
		return nil, err
	}
	target, err := callBackendResolve(t.backend, ctx, req)
	if err != nil {
		return nil, fmt.Errorf("resolve capture target: %w", err)
	}
	img, err := callBackendScreenshot(t.backend, ctx, target)
	if err != nil {
		return nil, fmt.Errorf("capture screenshot: %w", err)
	}
	if img == nil {
		return nil, fmt.Errorf("capture screenshot: backend returned no image")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}

	target.Info.Width = img.Bounds().Dx()
	target.Info.Height = img.Bounds().Dy()
	path, err := t.outputPath(ctx, args.Output, "screenshot-"+newID(), ".png")
	if err != nil {
		return nil, err
	}
	data := imageutil.EncodePNG(img)
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return nil, fmt.Errorf("create screenshot directory: %w", err)
	}
	if err := os.WriteFile(path, data, 0o644); err != nil {
		return nil, fmt.Errorf("write screenshot: %w", err)
	}
	preview, err := imageutil.OptimizeImage(img)
	if err != nil {
		return nil, fmt.Errorf("prepare screenshot preview: %w", err)
	}
	meta := struct {
		Action   string     `json:"action"`
		Target   TargetInfo `json:"target"`
		Output   string     `json:"output"`
		Bytes    int        `json:"bytes"`
		MimeType string     `json:"mime_type"`
	}{"screenshot", target.Info, path, len(data), "image/png"}
	text, _ := json.MarshalIndent(meta, "", "  ")
	return &tool.Result{Output: []*aop.Content{
		aop.Text(string(text)),
		aop.MediaData("image", preview.MimeType, filepath.Base(path), preview.Data),
	}}, nil
}

func (t *Tool) isClosed() bool {
	t.mu.RLock()
	defer t.mu.RUnlock()
	return t.closed
}
