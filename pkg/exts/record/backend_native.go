//go:build record && cgo && (windows || linux)

package record

/*
#cgo windows LDFLAGS: -lrecord -lgdi32 -lole32 -luser32 -lbcrypt -latomic
#cgo linux LDFLAGS: -lrecord -lm -ldl -lpthread
#include <stdlib.h>
#include "record_ffi.h"
*/
import "C"

import (
	"context"
	"fmt"
	"image"
	"math"
	"os"
	"runtime"
	"strings"
	"sync"
	"unsafe"

	recordtool "github.com/chainreactors/cyber/tools/record"
)

const nativeABIVersion = 1

type nativeTarget struct {
	value C.cyber_record_target
}

type nativeBackend struct {
	mu         sync.Mutex
	operations map[*C.cyber_record_operation]struct{}
	closed     bool
}

var _ recordtool.Backend = (*nativeBackend)(nil)

func newNativeBackend() (*nativeBackend, error) {
	if version := uint32(C.cyber_record_abi_version()); version != nativeABIVersion {
		return nil, fmt.Errorf("record native ABI version %d, want %d", version, nativeABIVersion)
	}
	return &nativeBackend{operations: make(map[*C.cyber_record_operation]struct{})}, nil
}

func (b *nativeBackend) Resolve(ctx context.Context, request recordtool.CaptureRequest) (recordtool.ResolvedTarget, error) {
	if err := ctx.Err(); err != nil {
		return recordtool.ResolvedTarget{}, err
	}
	if request.WindowHandle > math.MaxUint32 && runtime.GOOS == "linux" {
		return recordtool.ResolvedTarget{}, fmt.Errorf("X11 window ID 0x%x exceeds 32 bits", request.WindowHandle)
	}
	display := ""
	if runtime.GOOS == "linux" {
		if strings.EqualFold(strings.TrimSpace(os.Getenv("XDG_SESSION_TYPE")), "wayland") {
			return recordtool.ResolvedTarget{}, fmt.Errorf("Wayland capture is not supported; use an X11 session")
		}
		display = strings.TrimSpace(os.Getenv("DISPLAY"))
		if display == "" {
			return recordtool.ResolvedTarget{}, fmt.Errorf("DISPLAY is not set")
		}
	}
	if err := b.available(); err != nil {
		return recordtool.ResolvedTarget{}, err
	}
	cDisplay := C.CString(display)
	defer C.free(unsafe.Pointer(cDisplay))
	kind := C.uint32_t(C.CYBER_RECORD_TARGET_DESKTOP)
	if request.Target == "window" {
		kind = C.uint32_t(C.CYBER_RECORD_TARGET_WINDOW)
	}
	nativeRequest := C.cyber_record_target_request{
		kind:          kind,
		window_handle: C.uint64_t(request.WindowHandle),
		pid:           C.uint32_t(request.PID),
		display:       cDisplay,
	}
	var target C.cyber_record_target
	var errorBuffer [512]C.char
	if C.cyber_record_resolve_target(&nativeRequest, &target, &errorBuffer[0], C.size_t(len(errorBuffer))) != 0 {
		return recordtool.ResolvedTarget{}, nativeError(errorBuffer[:])
	}
	info := recordtool.TargetInfo{
		Kind:   request.Target,
		PID:    int64(target.pid),
		Title:  C.GoString(&target.title[0]),
		Width:  int(target.width),
		Height: int(target.height),
	}
	if target.window_handle != 0 {
		info.WindowHandle = fmt.Sprintf("0x%x", uint64(target.window_handle))
	}
	return recordtool.ResolvedTarget{Info: info, Native: nativeTarget{value: target}}, nil
}

func (b *nativeBackend) Screenshot(ctx context.Context, target recordtool.ResolvedTarget) (image.Image, error) {
	native, ok := target.Native.(nativeTarget)
	if !ok {
		return nil, fmt.Errorf("invalid native capture target")
	}
	var frame C.cyber_record_frame
	err := b.withOperation(ctx, func(operation *C.cyber_record_operation, errorBuffer *C.char, size C.size_t) C.int {
		return C.cyber_record_screenshot(operation, &native.value, 30, &frame, errorBuffer, size)
	})
	if err != nil {
		return nil, err
	}
	defer C.cyber_record_frame_free(&frame)
	if frame.data == nil || frame.width <= 0 || frame.height <= 0 || frame.stride < frame.width*4 {
		return nil, fmt.Errorf("native screenshot returned invalid pixels")
	}
	pixels := C.GoBytes(unsafe.Pointer(frame.data), C.int(frame.size))
	return &image.RGBA{
		Pix:    pixels,
		Stride: int(frame.stride),
		Rect:   image.Rect(0, 0, int(frame.width), int(frame.height)),
	}, nil
}

func (b *nativeBackend) Record(ctx context.Context, target recordtool.ResolvedTarget, output string, fps int) (recordtool.MediaInfo, error) {
	native, ok := target.Native.(nativeTarget)
	if !ok {
		return recordtool.MediaInfo{}, fmt.Errorf("invalid native capture target")
	}
	cOutput := C.CString(output)
	defer C.free(unsafe.Pointer(cOutput))
	var media C.cyber_record_media
	err := b.withOperation(ctx, func(operation *C.cyber_record_operation, errorBuffer *C.char, size C.size_t) C.int {
		return C.cyber_record_write_mp4(operation, &native.value, cOutput, C.int32_t(fps), &media, errorBuffer, size)
	})
	return recordtool.MediaInfo{Width: int(media.width), Height: int(media.height), Frames: int64(media.frames)}, err
}

func (b *nativeBackend) withOperation(ctx context.Context, call func(*C.cyber_record_operation, *C.char, C.size_t) C.int) error {
	if ctx == nil {
		ctx = context.Background()
	}
	operation := C.cyber_record_operation_new()
	if operation == nil {
		return fmt.Errorf("allocate native record operation")
	}
	b.mu.Lock()
	if b.closed {
		b.mu.Unlock()
		C.cyber_record_operation_free(operation)
		return fmt.Errorf("record native backend is closed")
	}
	b.operations[operation] = struct{}{}
	b.mu.Unlock()

	stop := make(chan struct{})
	watchDone := make(chan struct{})
	go func() {
		defer close(watchDone)
		select {
		case <-ctx.Done():
			C.cyber_record_operation_cancel(operation)
		case <-stop:
		}
	}()
	var errorBuffer [512]C.char
	result := call(operation, &errorBuffer[0], C.size_t(len(errorBuffer)))
	close(stop)
	<-watchDone
	b.mu.Lock()
	delete(b.operations, operation)
	b.mu.Unlock()
	C.cyber_record_operation_free(operation)
	if result == 0 {
		return nil
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	return nativeError(errorBuffer[:])
}

func (b *nativeBackend) available() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.closed {
		return fmt.Errorf("record native backend is closed")
	}
	return nil
}

func (b *nativeBackend) Close() error {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.closed = true
	for operation := range b.operations {
		C.cyber_record_operation_cancel(operation)
	}
	return nil
}

func nativeError(buffer []C.char) error {
	if len(buffer) == 0 {
		return fmt.Errorf("record native operation failed")
	}
	message := strings.TrimSpace(C.GoString(&buffer[0]))
	if message == "" {
		message = "record native operation failed"
	}
	return fmt.Errorf("%s", message)
}
