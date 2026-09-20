package console

import (
	"bytes"
	"context"
	"io"
	"strings"
	"sync"

	agentsession "github.com/chainreactors/cyber/agent/session"
	aop "github.com/chainreactors/cyber/aop"
	cfg "github.com/chainreactors/cyber/pkg/config"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
	rlterm "github.com/chainreactors/tui/readline/terminal"
)

func runRemoteConsole(ctx context.Context, rt *agentsession.Runtime, session *agentsession.Session, option *cfg.Option, input io.Reader, output io.Writer, control *rlterm.StreamControl, bindings *consoleapi.Bindings) error {
	if control == nil {
		control = rlterm.NewControl(true, 80, 24)
	}
	writer := &remoteTerminalWriter{w: output}
	return newAgentConsole(ctx, rt, session, option, rlterm.Stream(remoteTerminalReader{ctx, input}, writer, writer, control), bindings).Start()
}

// proc closes the input pipe on cancellation. Windows readline only recognizes
// EOF as an end of input, so translate canceled reads at the terminal boundary.
type remoteTerminalReader struct {
	ctx context.Context
	io.Reader
}

func (r remoteTerminalReader) Read(p []byte) (int, error) {
	if r.ctx.Err() != nil {
		return 0, io.EOF
	}
	n, err := r.Reader.Read(p)
	if err != nil && r.ctx.Err() != nil {
		err = io.EOF
	}
	return n, err
}

func isSessionBootstrapEvent(event *aop.Event) bool {
	if event == nil || event.TurnId != "" {
		return false
	}
	if message := event.GetMessage(); message != nil {
		return strings.HasPrefix(message.Id, "m-")
	}
	return event.GetToolResult() != nil
}

type remoteTerminalWriter struct {
	mu   sync.Mutex
	w    io.Writer
	last byte
	buf  bytes.Buffer
}

func (w *remoteTerminalWriter) Write(p []byte) (int, error) {
	w.mu.Lock()
	defer w.mu.Unlock()
	w.buf.Reset()
	w.buf.Grow(len(p) + len(p)/4)
	last := w.last
	for _, b := range p {
		if b == '\n' && last != '\r' {
			w.buf.WriteByte('\r')
		}
		w.buf.WriteByte(b)
		last = b
	}
	if w.buf.Len() > 0 {
		w.last = last
	}
	_, err := w.w.Write(w.buf.Bytes())
	return len(p), err
}
