package runner

import (
	"context"
	"fmt"
	"io"
	"sync"
	"time"

	rlterm "github.com/chainreactors/tui/readline/terminal"
	"github.com/chainreactors/utils/pty"
)

// AttachLocalREPL connects the process terminal to the Runtime-owned main REPL
// through the same PTY router used by WebSocket transport.
func (rt *AgentRuntime) AttachLocalREPL(ctx context.Context) error {
	router, err := rt.NewPTYRouter()
	if err != nil {
		return err
	}
	defer router.Close()

	terminal := rlterm.Local()
	restore, err := terminal.Control.MakeRaw()
	if err != nil {
		return err
	}
	defer restore()

	streamID := "local-repl"
	cols, rows := terminal.Control.Size()
	var sessionID string
	router.Handle(ctx, pty.Frame{Type: pty.FrameList, StreamID: streamID}, func(frame pty.Frame) {
		for _, session := range frame.Sessions {
			if session.State == pty.StateRunning && session.Kind == "repl" && session.Name == MainREPLName {
				sessionID = session.ID
				return
			}
		}
	})
	if sessionID == "" {
		return fmt.Errorf("main repl is not running")
	}

	done := make(chan error, 1)
	var writeMu sync.Mutex
	send := func(frame pty.Frame) {
		switch frame.Type {
		case pty.FrameOutput:
			writeMu.Lock()
			_, _ = terminal.Out.Write(frame.Data)
			writeMu.Unlock()
		case pty.FrameError:
			select {
			case done <- fmt.Errorf("pty: %s", frame.Error):
			default:
			}
		case pty.FrameClosed:
			select {
			case done <- nil:
			default:
			}
		}
	}
	router.Handle(ctx, pty.Frame{
		Type:      pty.FrameAttach,
		StreamID:  streamID,
		SessionID: sessionID,
		Cols:      cols,
		Rows:      rows,
	}, send)

	readErr := make(chan error, 1)
	go func() {
		buf := make([]byte, 4096)
		for {
			n, readErrValue := terminal.In.Read(buf)
			if n > 0 {
				data := append([]byte(nil), buf[:n]...)
				router.Handle(ctx, pty.Frame{Type: pty.FrameInput, StreamID: streamID, SessionID: sessionID, Data: data}, send)
			}
			if readErrValue != nil {
				readErr <- readErrValue
				return
			}
		}
	}()

	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
	lastCols, lastRows := cols, rows
	for {
		select {
		case err := <-done:
			return err
		case err := <-readErr:
			if err == io.EOF {
				return nil
			}
			return err
		case <-ticker.C:
			cols, rows := terminal.Control.Size()
			if cols != lastCols || rows != lastRows {
				lastCols, lastRows = cols, rows
				router.Handle(ctx, pty.Frame{Type: pty.FrameResize, StreamID: streamID, SessionID: sessionID, Cols: cols, Rows: rows}, send)
			}
		case <-ctx.Done():
			return ctx.Err()
		}
	}
}
