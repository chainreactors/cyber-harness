package tui

import (
	"bytes"
	"context"
	"fmt"
	"io"
	"sync"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	rlterm "github.com/chainreactors/tui/readline/terminal"
)

// AOPEventSubscriber connects a console-local renderer to the runtime AOP
// bus and returns an unsubscribe function owned by that console attachment.
type AOPEventSubscriber func(func(*aop.Event)) func()

// RunRemoteAgentConsoleWithControl adapts a byte-stream terminal while keeping
// event rendering scoped to the attached agent session.
func RunRemoteAgentConsoleWithControl(ctx context.Context, option *cfg.Option, appInfo AppInfo, session *agent.Agent, input io.Reader, output io.Writer, control *rlterm.StreamControl, subscribers ...AOPEventSubscriber) error {
	if control == nil {
		control = rlterm.NewControl(true, 80, 24)
	}
	terminal := &remoteTerminalWriter{w: output}
	return RunAgentConsoleWithTerminal(ctx, option, appInfo, session, rlterm.Stream(input, terminal, terminal, control), subscribers...)
}

// RunAgentConsoleWithTerminal creates the renderer and readline console for an
// explicit terminal. Local callers pass the process terminal directly so
// control sequences are never buffered and replayed through a PTY.
func RunAgentConsoleWithTerminal(ctx context.Context, option *cfg.Option, appInfo AppInfo, session *agent.Agent, terminal *rlterm.Terminal, subscribers ...AOPEventSubscriber) error {
	if terminal == nil {
		return fmt.Errorf("terminal is nil")
	}
	agentOutput := NewAgentOutputWithWriters(option, terminal.Out, terminal.Err, terminal.Control == nil || terminal.Control.IsTerminal())
	unsubscribe := subscribeAgentOutput(agentOutput, session, subscribers...)
	defer unsubscribe()
	repl := NewAgentConsoleWithTerminal(ctx, option, appInfo, session, agentOutput, terminal)
	return repl.Start()
}

// subscribeAgentOutput filters the shared runtime bus by session ID so a
// remote or local REPL cannot render sibling/subagent events accidentally.
func subscribeAgentOutput(output *AgentOutput, session *agent.Agent, subscribers ...AOPEventSubscriber) func() {
	if output == nil || session == nil || len(subscribers) == 0 || subscribers[0] == nil {
		return func() {}
	}
	sessionID := session.SessionID()
	return subscribers[0](func(event *aop.Event) {
		if sessionID == "" || event.SessionId == sessionID {
			output.HandleEvent(event)
		}
	})
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
