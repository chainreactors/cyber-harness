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

// bodyFile owns one capture file, its writer subscription and recording slot.
// captureState finishes it once, then releases the slot after publication.
type bodyFile struct {
	file        *os.File
	sub         *eventbus.Subscription[[]byte]
	slots       chan struct{}
	finishOnce  sync.Once
	releaseOnce sync.Once
	err         error
}

func (h *ProxyHub) captureBody(stream *bodyStream) (*bodyFile, error) {
	if h.stopping.Load() {
		return nil, errors.New("traffic: capture is stopping")
	}
	dir := h.store.BodyDir()
	if dir == "" {
		return nil, nil
	}
	select {
	case h.bodySlots <- struct{}{}:
	default:
		return nil, fmt.Errorf("traffic: concurrent body capture limit exceeded")
	}
	body := &bodyFile{slots: h.bodySlots}
	limit := h.storage.BodyMaxBytes
	if limit == 0 {
		limit = maxBodyCaptureBytes
	}
	remaining := limit
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
		if body.file == nil {
			bodyDir := filepath.Join(dir, "body")
			if err := os.MkdirAll(bodyDir, 0700); err != nil {
				return err
			}
			var err error
			body.file, err = os.CreateTemp(bodyDir, "capture-*")
			if err != nil {
				return err
			}
		}
		n, err := body.file.Write(p)
		if err == nil && n != len(p) {
			return io.ErrShortWrite
		}
		return err
	})
	if err != nil {
		body.release()
		return nil, err
	}
	body.sub = sub
	return body, nil
}

func (b *bodyFile) finish(discard bool) error {
	b.finishOnce.Do(func() {
		if discard {
			b.sub.Cancel()
		}
		err := b.sub.Close(context.Background())
		err = errors.Join(err, b.sub.Err())
		if b.file != nil {
			if !discard {
				err = errors.Join(err, b.file.Sync())
			}
			err = errors.Join(err, b.file.Close())
			if discard {
				err = errors.Join(err, os.Remove(b.file.Name()))
				b.file = nil
			}
		}
		b.err = err
	})
	return b.err
}

func (b *bodyFile) release() { b.releaseOnce.Do(func() { <-b.slots }) }

// Only handles files created by this adapter, never paths from a Flow.
func removeCaptureFiles(files [2]*os.File) {
	for _, file := range files {
		if file != nil {
			_ = file.Close()
			_ = os.Remove(file.Name())
		}
	}
}
