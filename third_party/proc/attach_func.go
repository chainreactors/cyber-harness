package proc

import (
	"context"
	"errors"
	"fmt"
	"io"
	"runtime/debug"
)

// Func runs an in-process function as a managed unit. There is no OS process,
// so the unit carries no pid and no exit code; cancelling the context the
// registry started it with is the only way to stop it, and a function that
// ignores that context is reported as abandoned rather than silently leaked.
func Func(fn func(ctx context.Context, w io.Writer) error) Attachment {
	return AttachFunc(func(ctx context.Context) (*Attached, error) {
		output, writer := io.Pipe()
		wait := runGuarded(func() error {
			defer writer.Close()
			return fn(ctx, writer)
		})
		return &Attached{Shape: ShapeFunc, Wait: wait, Output: output}, nil
	})
}

// IOFunc is Func for a function that also reads input, such as an interactive
// prompt hosted in this process. Pass a non-nil resize only when the function
// actually renders to a terminal.
func IOFunc(fn func(ctx context.Context, r io.Reader, w io.Writer) error, resize func(cols, rows int) error) Attachment {
	return AttachFunc(func(ctx context.Context) (*Attached, error) {
		input, inputWriter := io.Pipe()
		output, outputWriter := io.Pipe()

		// Unblock a function parked in Read when the registry cancels.
		stopped := make(chan struct{})
		go func() {
			select {
			case <-ctx.Done():
				_ = input.CloseWithError(ctx.Err())
			case <-stopped:
			}
		}()

		// EOF on the input side is how an interactive function ends cleanly,
		// so it is a completion rather than a failure.
		wait := runGuarded(func() error {
			defer close(stopped)
			defer outputWriter.Close()
			if err := fn(ctx, input, outputWriter); err != nil && !errors.Is(err, io.EOF) {
				return err
			}
			return nil
		})
		return &Attached{
			Shape:  ShapeFunc,
			Wait:   wait,
			Output: output,
			Input:  inputWriter,
			Resize: resize,
			Close:  func() error { return inputWriter.CloseWithError(io.EOF) },
		}, nil
	})
}

// runGuarded starts fn and returns a Wait that reports its outcome. A panic
// becomes the unit's error instead of taking the process down, which matters
// because units are started on behalf of whoever asked, not by the registry.
func runGuarded(fn func() error) func() Result {
	done := make(chan struct{})
	var err error
	go func() {
		defer close(done)
		defer func() {
			if recovered := recover(); recovered != nil {
				err = fmt.Errorf("panic: %v\n%s", recovered, debug.Stack())
			}
		}()
		err = fn()
	}()
	return func() Result {
		<-done
		return Result{Err: err}
	}
}
