package commands

import (
	"io"
	"strings"
	"sync"
)

// Output is the global output writer for commands.
// Commands write to it via fmt.Fprint(commands.Output, ...).
// The registry configures its destination before each execution.
var Output = &OutputWriter{w: io.Discard}

type OutputWriter struct {
	mu  sync.Mutex
	w   io.Writer
	buf strings.Builder
}

func (o *OutputWriter) Write(p []byte) (int, error) {
	o.mu.Lock()
	defer o.mu.Unlock()
	o.buf.Write(p)
	return o.w.Write(p)
}

func (o *OutputWriter) Captured() string {
	o.mu.Lock()
	defer o.mu.Unlock()
	return o.buf.String()
}

func (o *OutputWriter) Reset(w io.Writer) {
	o.mu.Lock()
	defer o.mu.Unlock()
	if w == nil {
		w = io.Discard
	}
	o.w = w
	o.buf.Reset()
}
