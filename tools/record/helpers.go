package record

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"math"
	"path/filepath"
	"runtime"
	"strconv"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/core/tool"
)

func normalizeDuration(seconds float64, required bool) (time.Duration, error) {
	if math.IsNaN(seconds) || math.IsInf(seconds, 0) {
		return 0, fmt.Errorf("duration_seconds must be a finite number")
	}
	if seconds < 0 {
		return 0, fmt.Errorf("duration_seconds cannot be negative")
	}
	if required && seconds == 0 {
		return 0, fmt.Errorf("duration_seconds must be greater than zero for action=record")
	}
	if seconds > float64(math.MaxInt64)/float64(time.Second) {
		return 0, fmt.Errorf("duration_seconds is too large")
	}
	duration := time.Duration(seconds * float64(time.Second))
	if seconds > 0 && duration <= 0 {
		return 0, fmt.Errorf("duration_seconds is too small")
	}
	return duration, nil
}

func normalizeCaptureArgs(args Args) (captureRequest, error) {
	if args.PID > int64(^uint32(0)) {
		return captureRequest{}, fmt.Errorf("pid exceeds the supported 32-bit process identifier range")
	}
	target := strings.ToLower(strings.TrimSpace(args.Target))
	if target == "" {
		target = "desktop"
	}
	fps := args.FPS
	if fps == 0 {
		fps = defaultFPS
	}
	if fps < 1 || fps > 60 {
		return captureRequest{}, fmt.Errorf("fps must be between 1 and 60")
	}
	req := captureRequest{Target: target, PID: args.PID, FPS: fps}
	if strings.TrimSpace(args.WindowHandle) != "" {
		value, err := strconv.ParseUint(strings.TrimSpace(args.WindowHandle), 0, 64)
		if err != nil || value == 0 {
			return captureRequest{}, fmt.Errorf("invalid window_handle %q", args.WindowHandle)
		}
		req.WindowHandle = value
	}
	switch target {
	case "desktop":
		if req.WindowHandle != 0 || req.PID != 0 {
			return captureRequest{}, fmt.Errorf("window_handle and pid require target=window")
		}
	case "window":
		if req.WindowHandle != 0 && req.PID != 0 {
			return captureRequest{}, fmt.Errorf("window_handle and pid are mutually exclusive")
		}
		if req.WindowHandle == 0 && req.PID <= 0 {
			return captureRequest{}, fmt.Errorf("target=window requires window_handle or pid")
		}
	default:
		return captureRequest{}, fmt.Errorf("unsupported target %q", args.Target)
	}
	return req, nil
}

func (t *Tool) outputPath(ctx context.Context, requested, base, ext string) (string, error) {
	path := strings.TrimSpace(requested)
	if path == "" {
		if invocationDir := tool.InvocationFromContext(ctx).WorkDir; invocationDir != "" {
			path = filepath.Join(invocationDir, ".aiscan", "record", base+ext)
		} else {
			path = filepath.Join(t.outputDir, base+ext)
		}
	} else {
		if filepath.Ext(path) == "" {
			path += ext
		} else if !strings.EqualFold(filepath.Ext(path), ext) {
			return "", fmt.Errorf("output must use %s extension", ext)
		}
		if !filepath.IsAbs(path) {
			path = filepath.Join(tool.WorkDirFromContext(ctx, t.workDir), path)
		}
	}
	abs, err := filepath.Abs(filepath.Clean(path))
	if err != nil {
		return "", fmt.Errorf("resolve output path: %w", err)
	}
	return abs, nil
}

func (t *Tool) mediaURI(ctx context.Context, path string) string {
	base := tool.WorkDirFromContext(ctx, t.workDir)
	if base != "" {
		if relative, err := filepath.Rel(base, path); err == nil && relative != ".." && !strings.HasPrefix(relative, ".."+string(filepath.Separator)) {
			return filepath.ToSlash(relative)
		}
	}
	return filepath.ToSlash(path)
}

func newID() string {
	buf := make([]byte, 8)
	if _, err := rand.Read(buf); err == nil {
		return hex.EncodeToString(buf)
	}
	return strconv.FormatInt(time.Now().UnixNano(), 36)
}

func jsonResult(value any) (*tool.Result, error) {
	return tool.TextResult(marshalJSON(value)), nil
}

func marshalJSON(value any) string {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return fmt.Sprintf(`{"error":%q}`, err.Error())
	}
	return string(data)
}

func samePath(a, b string) bool {
	aAbs, aErr := filepath.Abs(a)
	bAbs, bErr := filepath.Abs(b)
	if aErr == nil && bErr == nil {
		a, b = filepath.Clean(aAbs), filepath.Clean(bAbs)
	} else {
		a, b = filepath.Clean(a), filepath.Clean(b)
	}
	if runtime.GOOS == "windows" {
		return strings.EqualFold(a, b)
	}
	return a == b
}
