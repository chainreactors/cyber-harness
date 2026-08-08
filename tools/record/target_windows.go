//go:build record_ffmpeg && cgo && windows

package record

import (
	"context"
	"fmt"
	"syscall"
	"unsafe"

	"golang.org/x/sys/windows"
)

var (
	user32                       = windows.NewLazySystemDLL("user32.dll")
	procEnumWindows              = user32.NewProc("EnumWindows")
	procGetWindowThreadProcessID = user32.NewProc("GetWindowThreadProcessId")
	procIsWindow                 = user32.NewProc("IsWindow")
	procIsWindowVisible          = user32.NewProc("IsWindowVisible")
	procIsIconic                 = user32.NewProc("IsIconic")
	procGetWindowRect            = user32.NewProc("GetWindowRect")
	procGetWindowTextLengthW     = user32.NewProc("GetWindowTextLengthW")
	procGetWindowTextW           = user32.NewProc("GetWindowTextW")
	procGetSystemMetrics         = user32.NewProc("GetSystemMetrics")
	procGetForegroundWindow      = user32.NewProc("GetForegroundWindow")
)

type winRect struct {
	Left   int32
	Top    int32
	Right  int32
	Bottom int32
}

type winTargetInfo struct {
	handle uint64
	pid    int64
	title  string
	width  int
	height int
}

func resolvePlatformTarget(_ context.Context, req captureRequest) (resolvedTarget, error) {
	if req.Target == "desktop" {
		width, _, _ := procGetSystemMetrics.Call(78)
		height, _, _ := procGetSystemMetrics.Call(79)
		return resolvedTarget{
			Info:   TargetInfo{Kind: "desktop", Width: int(width), Height: int(height)},
			Native: nativeCaptureTarget{format: "gdigrab", url: "desktop"},
		}, nil
	}
	handle := req.WindowHandle
	if handle == 0 {
		resolved, err := findWindowByPID(uint32(req.PID))
		if err != nil {
			return resolvedTarget{}, err
		}
		handle = resolved.handle
	}
	info, err := inspectWindow(uintptr(handle))
	if err != nil {
		return resolvedTarget{}, err
	}
	return resolvedTarget{
		Info: TargetInfo{
			Kind:         "window",
			WindowHandle: fmt.Sprintf("0x%x", info.handle),
			PID:          info.pid,
			Title:        info.title,
			Width:        info.width,
			Height:       info.height,
		},
		Native: nativeCaptureTarget{format: "gdigrab", url: fmt.Sprintf("hwnd=0x%x", info.handle)},
	}, nil
}

func findWindowByPID(pid uint32) (winTargetInfo, error) {
	var best winTargetInfo
	callback := syscall.NewCallback(func(hwnd uintptr, _ uintptr) uintptr {
		info, err := inspectWindow(hwnd)
		if err != nil || uint32(info.pid) != pid {
			return 1
		}
		if info.width*info.height > best.width*best.height {
			best = info
		}
		return 1
	})
	result, _, callErr := procEnumWindows.Call(callback, 0)
	if result == 0 {
		return winTargetInfo{}, fmt.Errorf("enumerate windows for pid %d: %w", pid, callErr)
	}
	if best.handle == 0 {
		return winTargetInfo{}, fmt.Errorf("no visible non-minimized top-level window found for pid %d", pid)
	}
	return best, nil
}

func inspectWindow(hwnd uintptr) (winTargetInfo, error) {
	if ok, _, _ := procIsWindow.Call(hwnd); ok == 0 {
		return winTargetInfo{}, fmt.Errorf("window handle 0x%x does not exist", hwnd)
	}
	if visible, _, _ := procIsWindowVisible.Call(hwnd); visible == 0 {
		return winTargetInfo{}, fmt.Errorf("window 0x%x is not visible", hwnd)
	}
	if iconic, _, _ := procIsIconic.Call(hwnd); iconic != 0 {
		return winTargetInfo{}, fmt.Errorf("window 0x%x is minimized", hwnd)
	}
	var rect winRect
	if ok, _, callErr := procGetWindowRect.Call(hwnd, uintptr(unsafe.Pointer(&rect))); ok == 0 {
		return winTargetInfo{}, fmt.Errorf("get window rect 0x%x: %w", hwnd, callErr)
	}
	width, height := int(rect.Right-rect.Left), int(rect.Bottom-rect.Top)
	if width <= 0 || height <= 0 {
		return winTargetInfo{}, fmt.Errorf("window 0x%x has invalid dimensions %dx%d", hwnd, width, height)
	}
	var pid uint32
	procGetWindowThreadProcessID.Call(hwnd, uintptr(unsafe.Pointer(&pid)))
	length, _, _ := procGetWindowTextLengthW.Call(hwnd)
	var title string
	if length > 0 {
		buf := make([]uint16, length+1)
		procGetWindowTextW.Call(hwnd, uintptr(unsafe.Pointer(&buf[0])), length+1)
		title = windows.UTF16ToString(buf)
	}
	return winTargetInfo{handle: uint64(hwnd), pid: int64(pid), title: title, width: width, height: height}, nil
}
