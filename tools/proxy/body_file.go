package proxy

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"sync"

	"github.com/chainreactors/aiscan/core/eventbus"
)

// recordBody attaches a file consumer and returns its finalizer and slot
// release. The file is worker-owned until drain completes. No file I/O runs
// during registration or on the proxy reader.
func (h *ProxyHub) recordBody(stream *bodyStream) (func(bool) (*os.File, error), func(), error) {
	if h.stopping.Load() {
		return nil, nil, errors.New("traffic: capture is stopping")
	}
	dir := h.store.BodyDir()
	if dir == "" {
		return nil, nil, nil
	}
	select {
	case h.bodySlots <- struct{}{}:
	default:
		return nil, nil, fmt.Errorf("traffic: concurrent body recorder limit exceeded")
	}
	var releaseOnce sync.Once
	release := func() { releaseOnce.Do(func() { <-h.bodySlots }) }
	limit := h.storage.BodyMaxBytes
	if limit == 0 {
		limit = maxBodyCaptureBytes
	}
	remaining := limit
	var file *os.File
	sub, err := stream.events.SubscribeAsync(eventbus.SubscribeOptions[[]byte]{
		Buffer: 128, MaxBytes: 1 << 20,
		Filter: func([]byte) bool { return remaining > 0 },
		Size:   func(p []byte) int64 { return min(int64(len(p)), remaining) },
		Clone: func(p []byte) []byte {
			n := min(int64(len(p)), remaining)
			remaining -= n
			return append([]byte(nil), p[:n]...)
		},
	}, func(p []byte) error {
		if file == nil {
			bodyDir := filepath.Join(dir, "body")
			if err := os.MkdirAll(bodyDir, 0700); err != nil {
				return err
			}
			var err error
			file, err = os.CreateTemp(bodyDir, "capture-*")
			if err != nil {
				return err
			}
		}
		n, err := file.Write(p)
		if err == nil && n != len(p) {
			return io.ErrShortWrite
		}
		return err
	})
	if err != nil {
		release()
		return nil, nil, err
	}
	var once sync.Once
	var result error
	finish := func(discard bool) (*os.File, error) {
		once.Do(func() {
			if discard {
				sub.Cancel()
			}
			result = sub.Close(context.Background())
			if file != nil {
				if !discard {
					result = errors.Join(result, file.Sync())
				}
				result = errors.Join(result, file.Close())
				if discard {
					result = errors.Join(result, os.Remove(file.Name()))
					file = nil
				}
			}
		})
		return file, result
	}
	return finish, release, nil
}

// Only handles files created by this adapter, never paths from a Flow.
func removeCaptureFiles(files [2]*os.File) {
	for _, file := range files {
		if file != nil {
			_ = file.Close()
			_ = os.Remove(file.Name())
		}
	}
}
