package proc

import (
	"errors"
	"os/exec"
)

// ErrUnsupported is returned by a capability the shape does not have. A stop
// ladder treats it as "this rung did nothing" and escalates.
var ErrUnsupported = errors.New("proc: unsupported for this unit")

// command builds the process described by o. It never sets Cancel: context
// cancellation must stay inert for OS-backed units so that the stop ladder is
// the only path that terminates them.
func command(o ProcOptions) *exec.Cmd {
	var cmd *exec.Cmd
	if o.Line != "" {
		cmd = ShellCommand(o.Line)
	} else {
		cmd = exec.Command(o.Binary, o.Args...)
	}
	cmd.Dir = o.Dir
	if env := o.environ(); env != nil {
		cmd.Env = env
	}
	return cmd
}

// resultFromWait converts a process wait error into a terminal Result. A
// process always has a real exit status, so Exited is true whenever one was
// reported; it is false only when the process could not be waited on at all.
func resultFromWait(err error) Result {
	if err == nil {
		return Result{Exited: true, ExitCode: 0}
	}
	var exitErr *exec.ExitError
	if errors.As(err, &exitErr) {
		return Result{
			Err:      err,
			Exited:   true,
			ExitCode: exitErr.ExitCode(),
			Signal:   exitSignal(exitErr),
		}
	}
	// Some pty backends wrap the status in their own type.
	var coded interface{ ExitCode() int }
	if errors.As(err, &coded) {
		return Result{Err: err, Exited: true, ExitCode: coded.ExitCode()}
	}
	return Result{Err: err}
}
