package task

import (
	"fmt"
	"os"
	"regexp"
	"strings"
	"sync"
)

const DefaultBufferCap = 2 * 1024 * 1024 // 2MB

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*[a-zA-Z]|\x1b\].*?\x07|\x1b\(B`)

type OutputBuffer struct {
	mu         sync.Mutex
	buf        []byte
	cap        int
	baseOffset int64
	onWrite    func([]byte)
	file       *os.File // optional: tee writes to file alongside memory
	stripANSI  bool
}

func NewOutputBuffer(cap int) *OutputBuffer {
	if cap <= 0 {
		cap = DefaultBufferCap
	}
	return &OutputBuffer{
		buf: make([]byte, 0, min(cap, 64*1024)),
		cap: cap,
	}
}

func NewOutputBufferWithFile(cap int, path string) (*OutputBuffer, error) {
	b := NewOutputBuffer(cap)
	f, err := os.OpenFile(path, os.O_CREATE|os.O_WRONLY|os.O_APPEND, 0o600)
	if err != nil {
		return nil, fmt.Errorf("open output file: %w", err)
	}
	b.file = f
	return b, nil
}

func (b *OutputBuffer) Write(p []byte) (int, error) {
	n := len(p)
	if b.stripANSI {
		p = ansiRe.ReplaceAll(p, nil)
	}
	b.mu.Lock()
	b.buf = append(b.buf, p...)
	if len(b.buf) > b.cap {
		excess := len(b.buf) - b.cap
		b.baseOffset += int64(excess)
		fresh := make([]byte, b.cap)
		copy(fresh, b.buf[excess:])
		b.buf = fresh
	}
	if b.file != nil {
		_, _ = b.file.Write(p)
	}
	cb := b.onWrite
	b.mu.Unlock()
	if cb != nil {
		cb(p)
	}
	return n, nil
}

func (b *OutputBuffer) Close() {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.file != nil {
		b.file.Close()
		b.file = nil
	}
}

func (b *OutputBuffer) TailLines(n int) string {
	b.mu.Lock()
	s := string(b.buf)
	b.mu.Unlock()
	return tailLines(s, n)
}

func (b *OutputBuffer) ReadSince(offset int64) ([]byte, int64) {
	data, newOff, _, _ := b.ReadSinceLimit(offset, 0)
	return data, newOff
}

func (b *OutputBuffer) ReadSinceLimit(offset int64, maxBytes int64) ([]byte, int64, bool, error) {
	b.mu.Lock()
	defer b.mu.Unlock()

	end := b.baseOffset + int64(len(b.buf))
	if offset < b.baseOffset {
		offset = b.baseOffset
	}
	if offset >= end {
		return nil, end, false, nil
	}
	start := int(offset - b.baseOffset)
	avail := b.buf[start:]
	more := false
	if maxBytes > 0 && int64(len(avail)) > maxBytes {
		avail = avail[:maxBytes]
		more = true
	}
	out := make([]byte, len(avail))
	copy(out, avail)
	return out, offset + int64(len(out)), more, nil
}

func (b *OutputBuffer) Bytes() []byte {
	b.mu.Lock()
	defer b.mu.Unlock()
	out := make([]byte, len(b.buf))
	copy(out, b.buf)
	return out
}

func (b *OutputBuffer) Len() int64 {
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.baseOffset + int64(len(b.buf))
}

func (b *OutputBuffer) AppendError(msg string) {
	_, _ = b.Write([]byte("\n[task error] " + msg + "\n"))
}

func tailLines(s string, n int) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	s = strings.ReplaceAll(s, "\r", "\n")
	lines := strings.Split(strings.TrimRight(s, "\n"), "\n")
	kept := make([]string, 0, len(lines))
	for _, ln := range lines {
		if strings.TrimSpace(ln) == "" {
			continue
		}
		kept = append(kept, ln)
	}
	if len(kept) > n {
		kept = kept[len(kept)-n:]
	}
	return strings.Join(kept, "\n")
}

func (b *OutputBuffer) String() string {
	b.mu.Lock()
	defer b.mu.Unlock()
	return string(b.buf)
}
