package traffic

import (
	"crypto/sha256"
	"encoding/hex"
	"fmt"
	"hash"
	"io"
	"os"
	"path/filepath"
	"sync"
)

// BodyRef describes a body kept outside the hot Exchange value. Body contains
// only the configured preview; callers that need the complete payload can
// hydrate it from Path.
type BodyRef struct {
	Path      string `json:"path,omitempty"`
	Size      int64  `json:"size"`
	SHA256    string `json:"sha256,omitempty"`
	Complete  bool   `json:"complete"`
	Truncated bool   `json:"truncated,omitempty"`
}

// BodySink writes a body to a temporary file while retaining a small preview.
// It is safe for a single reader goroutine and Close is idempotent.
type BodySink struct {
	mu         sync.Mutex
	partPath   string
	finalPath  string
	file       *os.File
	hash       hash.Hash
	size       int64
	preview    []byte
	previewMax int
	err        error
	closed     bool
	complete   bool
}

// NewBodySink creates <name>.part in dir and atomically publishes it as name
// when Close(true) succeeds. The directory is created when needed.
func NewBodySink(dir, name string, previewMax int) (*BodySink, error) {
	if dir == "" {
		return nil, fmt.Errorf("traffic: body directory is empty")
	}
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, fmt.Errorf("traffic: create body directory: %w", err)
	}
	finalPath := filepath.Join(dir, name)
	partPath := finalPath + ".part"
	f, err := os.OpenFile(partPath, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0o600)
	if err != nil {
		return nil, fmt.Errorf("traffic: create body file: %w", err)
	}
	if previewMax < 0 {
		previewMax = 0
	}
	h := sha256.New()
	return &BodySink{
		partPath:   partPath,
		finalPath:  finalPath,
		file:       f,
		hash:       h,
		previewMax: previewMax,
	}, nil
}

// Write appends p to the body file and updates its preview and digest.
func (s *BodySink) Write(p []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		if s.err != nil {
			return 0, s.err
		}
		return 0, io.ErrClosedPipe
	}
	if s.err != nil {
		return 0, s.err
	}
	n, err := s.file.Write(p)
	if n > 0 {
		s.size += int64(n)
		_, _ = s.hash.Write(p[:n])
		if len(s.preview) < s.previewMax {
			end := len(p)
			if remaining := s.previewMax - len(s.preview); end > remaining {
				end = remaining
			}
			s.preview = append(s.preview, p[:end]...)
		}
	}
	if err != nil {
		s.err = err
	}
	return n, err
}

// Reader wraps r so body bytes are captured as they pass through the proxy.
func (s *BodySink) Reader(r io.Reader) io.Reader {
	return &bodyReader{src: r, sink: s}
}

type bodyReader struct {
	src  io.Reader
	sink *BodySink
}

func (r *bodyReader) Read(p []byte) (int, error) {
	n, err := r.src.Read(p)
	if n > 0 {
		// Capture failures are recorded by BodySink but deliberately do not
		// change the upstream reader result: observation must not alter the
		// request/response being proxied.
		_, _ = r.sink.Write(p[:n])
	}
	return n, err
}

// Close finishes the body. A failed or incomplete capture keeps its .part
// file for diagnosis and reports Complete=false in the returned reference.
func (s *BodySink) Close(complete bool) (BodyRef, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return s.refLocked(), s.err
	}
	s.closed = true
	s.complete = complete && s.err == nil
	if s.file != nil {
		if err := s.file.Sync(); err != nil && s.err == nil {
			s.err = err
			s.complete = false
		}
		if err := s.file.Close(); err != nil && s.err == nil {
			s.err = err
			s.complete = false
		}
	}
	if s.complete {
		if err := os.Rename(s.partPath, s.finalPath); err != nil {
			s.err = err
			s.complete = false
		}
	}
	return s.refLocked(), s.err
}

// Preview returns a copy of the bytes retained for list/detail summaries.
func (s *BodySink) Preview() []byte {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]byte(nil), s.preview...)
}

// Discard closes and removes a capture that was filtered out before it became
// a visible Exchange.
func (s *BodySink) Discard() error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.closed {
		s.closed = true
		_ = s.file.Close()
	}
	if err := os.Remove(s.partPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	if err := os.Remove(s.finalPath); err != nil && !os.IsNotExist(err) {
		return err
	}
	return nil
}

func (s *BodySink) refLocked() BodyRef {
	digest := ""
	if s.hash != nil {
		digest = hex.EncodeToString(s.hash.Sum(nil))
	}
	path := s.partPath
	if s.complete {
		path = s.finalPath
	}
	return BodyRef{
		Path:      path,
		Size:      s.size,
		SHA256:    digest,
		Complete:  s.complete,
		Truncated: false,
	}
}

// ReadBody loads a body reference on demand. It intentionally does not cache
// the bytes in the reference so callers control the resulting allocation.
func ReadBody(ref *BodyRef) ([]byte, error) {
	if ref == nil || ref.Path == "" {
		return nil, nil
	}
	return os.ReadFile(ref.Path)
}
