package console

import (
	"bytes"
	"context"
	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	sessionext "github.com/chainreactors/aiscan/pkg/exts/session"
	rlterm "github.com/chainreactors/tui/readline/terminal"
	"io"
	"strings"
	"sync"
)

func runRemoteConsole(ctx context.Context, rt *sessionext.Runtime, session *sessionext.Session, option *cfg.Option, input io.Reader, output io.Writer, control *rlterm.StreamControl) error {
	if control == nil {
		control = rlterm.NewControl(true, 80, 24)
	}
	writer := &remoteTerminalWriter{w: output}
	return newAgentConsole(ctx, rt, session, option, rlterm.Stream(input, writer, writer, control)).Start()
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
