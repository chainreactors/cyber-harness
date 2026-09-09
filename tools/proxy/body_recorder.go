package proxy

import (
	"context"
	"errors"
	"sync"

	traffic "github.com/chainreactors/aiscan/aop/traffic"
	"github.com/chainreactors/aiscan/core/eventbus"
)

// bodyRecorder observes chunks through a bounded subscription. File creation,
// writes and finalization belong to its consumer, never to the proxy reader.
// At most 16 recorders are admitted per store, each with a 1 MiB queue.
// Saturation stops only that body's recorder and is reported on its Flow.
type bodyRecorder struct {
	mu                    sync.Mutex
	bus                   eventbus.Bus[[]byte]
	sub                   *eventbus.Subscription[[]byte]
	sink                  *traffic.BodySink // worker-owned until sub.Done
	preview               []byte
	size, admitted, limit int64
	closed                bool
	ref                   traffic.BodyRef
	err                   error
	release               func()
}

func newBodyRecorder(dir, name string, limit int64, release func()) (*bodyRecorder, error) {
	var once sync.Once
	r := &bodyRecorder{limit: limit, release: func() { once.Do(release) }}
	sub, err := r.bus.SubscribeAsync(eventbus.SubscribeOptions[[]byte]{
		Buffer: 128, MaxBytes: 1 << 20,
		Size:  func(p []byte) int64 { return int64(len(p)) },
		Clone: func(p []byte) []byte { return append([]byte(nil), p...) },
	}, func(p []byte) error {
		if r.sink == nil {
			var err error
			r.sink, err = traffic.NewBodySinkWithLimit(dir, name, maxBodySnip, limit)
			if err != nil {
				return err
			}
		}
		_, err := r.sink.Write(p)
		return err
	})
	if err != nil {
		release()
		return nil, err
	}
	r.sub = sub
	return r, nil
}

func (r *bodyRecorder) Write(p []byte) (int, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return len(p), nil // observation must never change forwarding
	}
	n := len(p)
	r.size += int64(n)
	r.preview = appendPreview(r.preview, p, maxBodySnip)
	// Do not copy bytes that cannot be retained. Split large buffered bodies so
	// a single event cannot retain the proxy's entire backing allocation.
	for r.sub != nil && len(p) > 0 && r.admitted < r.limit && r.sub.Err() == nil {
		count := min(len(p), 32<<10)
		if int64(count) > r.limit-r.admitted {
			count = int(r.limit - r.admitted)
		}
		r.bus.Emit(p[:count])
		r.admitted += int64(count)
		p = p[count:]
	}
	return n, nil
}

func (r *bodyRecorder) Preview() []byte {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]byte(nil), r.preview...)
}

// Close is called by capture finalization off the proxy path. A reference is
// published only after the subscriber drains and the file is closed/renamed.
func (r *bodyRecorder) Close(complete bool) (traffic.BodyRef, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.closed {
		return r.ref, r.err
	}
	r.closed = true
	if r.sub != nil {
		r.err = r.sub.Close(context.Background())
	}
	if r.sink != nil {
		var err error
		r.ref, err = r.sink.Close(complete && r.err == nil)
		r.err = errors.Join(r.err, err)
	}
	r.ref.Size = r.size
	if r.sub == nil {
		r.ref.StoredSize = int64(len(r.preview))
	}
	r.ref.Truncated = r.ref.StoredSize < r.size
	r.ref.Complete = complete && r.err == nil
	return r.ref, r.err
}

func (r *bodyRecorder) Discard() error {
	defer r.release()
	if r.sub != nil {
		r.sub.Cancel()
	}
	_, err := r.Close(false)
	if r.sink != nil {
		err = errors.Join(err, r.sink.Discard())
	}
	return err
}
