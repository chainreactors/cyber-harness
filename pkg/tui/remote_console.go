package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/agent"
	rlterm "github.com/reeflective/readline/terminal"
)

// RunRemoteAgentConsole runs the agent console over a byte-stream transport.
// The transport provides raw terminal input and receives terminal output.
func RunRemoteAgentConsole(ctx context.Context, option *cfg.Option, appInfo AppInfo, session *agent.Agent, input io.Reader, output io.Writer, bus ...*eventbus.Bus[agent.Event]) error {
	if option == nil {
		option = &cfg.Option{}
	}
	if input == nil {
		return fmt.Errorf("remote console input is nil")
	}
	if output == nil {
		output = io.Discard
	}

	control := rlterm.NewControl(true, 80, 24)
	return RunRemoteAgentConsoleWithControl(ctx, option, appInfo, session, input, output, control, bus...)
}

func RunRemoteAgentConsoleWithControl(ctx context.Context, option *cfg.Option, appInfo AppInfo, session *agent.Agent, input io.Reader, output io.Writer, control *rlterm.StreamControl, bus ...*eventbus.Bus[agent.Event]) error {
	if control == nil {
		control = rlterm.NewControl(true, 80, 24)
	}
	terminal := &remoteTerminalWriter{w: output}
	return RunAgentConsoleWithTerminal(ctx, option, appInfo, session, rlterm.Stream(input, terminal, terminal, control), bus...)
}

func RunAgentConsoleWithTerminal(ctx context.Context, option *cfg.Option, appInfo AppInfo, session *agent.Agent, terminal *rlterm.Terminal, bus ...*eventbus.Bus[agent.Event]) error {
	if terminal == nil {
		return fmt.Errorf("terminal is nil")
	}
	agentOutput := NewAgentOutputWithWriters(option, terminal.Out, terminal.Err, terminal.Control == nil || terminal.Control.IsTerminal())
	repl := NewAgentConsoleWithTerminal(ctx, option, appInfo, session, agentOutput, terminal, bus...)
	return repl.Start()
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

