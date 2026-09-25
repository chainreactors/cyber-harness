package proc

import (
	"context"
	"errors"
	"io"
	"os"
)

// Pipe runs a process on plain pipes: byte-exact streams, separate stdout and
// stderr, stdin, no resize. This is the shape for a child that speaks a
// protocol, where a pseudo-terminal's echo, newline translation and line
// discipline would corrupt the bytes.
//
// With RawStdout the protocol stream is handed to a single owner through
// Manager.Raw and only stderr reaches the diagnostic ring buffer. Without it
// both streams share one OS pipe, so the buffer sees them interleaved -- which
// is what you want for diagnostics and never for a protocol.
func Pipe(o ProcOptions) Attachment {
	return AttachFunc(func(context.Context) (*Attached, error) {
		cmd := command(o)
		setProcessGroup(cmd)

		// Own every pipe rather than using Cmd.StdinPipe and friends: those are
		// closed by Cmd.Wait, which would truncate a stream the supervisor has
		// not drained yet.
		var closers []io.Closer
		closeAll := func() {
			for _, c := range closers {
				_ = c.Close()
			}
		}

		stdinRead, stdinWrite, err := os.Pipe()
		if err != nil {
			return nil, err
		}
		closers = append(closers, stdinRead, stdinWrite)
		cmd.Stdin = stdinRead

		diagRead, diagWrite, err := os.Pipe()
		if err != nil {
			closeAll()
			return nil, err
		}
		closers = append(closers, diagRead, diagWrite)
		cmd.Stderr = diagWrite

		var rawRead, rawWrite *os.File
		if o.RawStdout {
			rawRead, rawWrite, err = os.Pipe()
			if err != nil {
				closeAll()
				return nil, err
			}
			closers = append(closers, rawRead, rawWrite)
			cmd.Stdout = rawWrite
		} else {
			// One descriptor for both streams: os/exec reuses the stdout file
			// for stderr when they are the same value, so the child's writes
			// are merged by the kernel instead of by two racing goroutines.
			cmd.Stdout = diagWrite
		}

		if err := cmd.Start(); err != nil {
			closeAll()
			return nil, err
		}

		// Drop the parent's copies of the child's ends. The child must be the
		// only writer, or the reader never sees EOF.
		_ = stdinRead.Close()
		_ = diagWrite.Close()
		if rawWrite != nil {
			_ = rawWrite.Close()
		}

		pid := 0
		if cmd.Process != nil {
			pid = cmd.Process.Pid
		}

		attached := &Attached{
			Shape:  ShapePipe,
			Wait:   func() Result { return resultFromWait(cmd.Wait()) },
			Signal: func(sig Signal) error { return signalProcessGroup(pid, sig) },
			Output: diagRead,
			Input:  stdinWrite,
			Ready:  o.Ready,
			Live:   o.Live,
			Proc:   &Proc{PID: pid},
			Close: func() error {
				// Leave diagRead open: the pump ends on EOF once the child is
				// gone, and closing it here would discard buffered output. The
				// supervisor bounds that drain in case a grandchild inherited
				// the descriptor.
				err := stdinWrite.Close()
				if rawRead != nil {
					err = errors.Join(err, rawRead.Close())
				}
				return err
			},
		}
		if rawRead != nil {
			attached.Raw = rawRead
		}
		return attached, nil
	})
}
