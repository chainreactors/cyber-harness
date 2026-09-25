package proc

import (
	"context"
	"io"
)

// etx is Ctrl+C as the line discipline sees it.
const etx = 0x03

// TTY runs a process on a pseudo-terminal: one merged output stream, key input
// and resize. RawStdout is meaningless here -- a terminal has a single stream --
// and is ignored.
func TTY(o ProcOptions) Attachment {
	return AttachFunc(func(context.Context) (*Attached, error) {
		// No setProcessGroup here: opening a pseudo-terminal already puts the
		// child in a new session, and asking for a process group on top of that
		// fails with EPERM. The child is the session leader, so signalling its
		// negated pid still addresses the whole group.
		handle, err := startPTY(command(o))
		if err != nil {
			return nil, err
		}
		return &Attached{
			Shape:  ShapeTTY,
			Wait:   func() Result { return resultFromWait(handle.Wait()) },
			Signal: ttySignal(handle),
			Output: handle,
			Input:  writeNopCloser{handle},
			Resize: handle.Resize,
			Ready:  o.Ready,
			Live:   o.Live,
			Proc:   &Proc{PID: handle.PID()},
			Close:  handle.Close,
		}, nil
	})
}

// ttySignal delivers the interrupt rung through the terminal rather than to the
// process group. The line discipline hands ETX to the foreground job, which is
// the process the operator means; signalling the group would hit the shell
// instead. It also works identically on every platform, where a console break
// event does not.
func ttySignal(handle *ptyHandle) func(Signal) error {
	return func(sig Signal) error {
		if sig == SigInterrupt {
			_, err := handle.Write([]byte{etx})
			return err
		}
		return signalProcessGroup(handle.PID(), sig)
	}
}

// writeNopCloser lets a terminal serve as Attached.Input without exposing a
// Close that would tear down the whole session.
type writeNopCloser struct{ w io.Writer }

func (c writeNopCloser) Write(p []byte) (int, error) { return c.w.Write(p) }

func (writeNopCloser) Close() error { return nil }
