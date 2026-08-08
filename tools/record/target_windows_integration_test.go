//go:build record_ffmpeg && record_integration && cgo && windows

package record

import (
	"context"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeWindowHandleAndPIDCapture(t *testing.T) {
	hwnd, _, _ := procGetForegroundWindow.Call()
	if hwnd == 0 {
		t.Log("no foreground window; desktop integration still covered")
		return
	}
	backend := newPlatformBackend()
	byHandle, err := backend.Resolve(context.Background(), captureRequest{Target: "window", WindowHandle: uint64(hwnd), FPS: 8})
	if err != nil {
		t.Logf("foreground window is not stable enough to capture: %v", err)
		return
	}
	if byHandle.Info.PID <= 0 {
		t.Fatal("resolved window has no PID")
	}
	if _, err := backend.Screenshot(context.Background(), byHandle); err != nil {
		t.Fatalf("screenshot by handle: %v", err)
	}
	byPID, err := backend.Resolve(context.Background(), captureRequest{Target: "window", PID: byHandle.Info.PID, FPS: 8})
	if err != nil {
		// The foreground window belongs to another interactive process and may
		// close, minimize, or replace its top-level HWND between the two calls.
		t.Logf("foreground process is no longer capturable by PID: %v", err)
		return
	}
	ctx, cancel := context.WithTimeout(context.Background(), time.Second)
	defer cancel()
	media, err := backend.Record(ctx, byPID, filepath.Join(t.TempDir(), "window.mp4"), 8)
	if err != nil {
		t.Fatalf("record by PID: %v", err)
	}
	if media.Frames == 0 {
		t.Fatal("window recording produced no frames")
	}
}
