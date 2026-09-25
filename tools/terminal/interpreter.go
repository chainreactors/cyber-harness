package terminal

import (
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"

	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/utils/proc"
	"mvdan.cc/sh/v3/expand"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

func (t *BashTool) interpreterAttachment(script *syntax.File, execution *coretool.Execution, options BashExecOptions) proc.Attachment {
	return proc.AttachFunc(func(ctx context.Context) (*proc.Attached, error) {
		input, inputWriter := io.Pipe()
		output, outputWriter := io.Pipe()
		stdin := io.Reader(input)
		if options.Stdin != nil {
			stdin = options.Stdin
		}
		done := make(chan proc.Result, 1)
		go func() {
			defer outputWriter.Close()
			defer input.Close()
			stdout := joinedWriter(outputWriter, options.Stdout)
			stderr := joinedWriter(outputWriter, options.Stderr)
			runner, err := interp.New(
				interp.Dir(execution.Dir),
				interp.Env(expand.ListEnviron(append(os.Environ(), execution.Env...)...)),
				interp.StdIO(stdin, stdout, stderr),
				interp.ExecHandler(func(callCtx context.Context, argv []string) error {
					hc := interp.HandlerCtx(callCtx)
					id, err := execution.WaitID(callCtx)
					if err != nil {
						return err
					}
					parent := &coretool.Execution{
						ID: id, Dir: hc.Dir, Env: exportedShellEnv(hc.Env),
						Stdin: hc.Stdin, Stdout: hc.Stdout, Stderr: hc.Stderr,
					}
					details, runErr := coretool.RunCommand(callCtx, t.registry, argv, parent)
					if execution.Command == argv[0] {
						execution.SetDetails(details)
					}
					if runErr == nil {
						return nil
					}
					var exit *exec.ExitError
					if errors.As(runErr, &exit) {
						return interp.ExitStatus(exit.ExitCode())
					}
					var coded interface{ ExitCode() int }
					if errors.As(runErr, &coded) {
						return interp.ExitStatus(coded.ExitCode())
					}
					if errors.Is(runErr, exec.ErrNotFound) {
						fmt.Fprintln(hc.Stderr, runErr)
						return interp.ExitStatus(127)
					}
					if callCtx.Err() != nil {
						return callCtx.Err()
					}
					fmt.Fprintln(hc.Stderr, runErr)
					return interp.ExitStatus(1)
				}),
			)
			if err == nil {
				err = runner.Run(ctx, script)
			}
			status := 0
			var exit interp.ExitStatus
			if errors.As(err, &exit) {
				status = int(exit)
			} else if err != nil {
				status = 1
				if ctx.Err() == nil {
					fmt.Fprintln(stderr, err)
				}
			}
			done <- proc.Result{Err: err, Status: &status}
		}()
		stop := context.AfterFunc(ctx, func() { _ = input.CloseWithError(ctx.Err()) })
		return &proc.Attached{
			Shape:  proc.ShapeFunc,
			Wait:   func() proc.Result { return <-done },
			Output: output, Input: inputWriter,
			Close: func() error { stop(); return inputWriter.Close() },
		}, nil
	})
}

func exportedShellEnv(env expand.Environ) []string {
	var values []string
	env.Each(func(name string, value expand.Variable) bool {
		if value.IsSet() && value.Exported {
			values = append(values, name+"="+value.String())
		}
		return true
	})
	return values
}
