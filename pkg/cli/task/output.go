// Package task owns the CLI presentation boundary around one-shot tasks.
package task

import (
	"context"
	"errors"
	"io"
	"os"
	"path/filepath"
	"time"

	agentsession "github.com/chainreactors/cyber/agent/session"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/eventbus"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/console"
	outputpkg "github.com/chainreactors/cyber/pkg/output"
	flags "github.com/jessevdk/go-flags"
)

// Output owns a CLI's output from option resolution through task completion.
// Before Run, Finish emits startup errors. Run transfers presentation to Console,
// which includes finalization failures in its single final result.
type Output struct {
	option         *cfg.Option
	stdout, stderr io.Writer
	started        bool
	file           *os.File
	renderer       *outputpkg.EventWriter
	sub            *eventbus.Subscription[*aop.Event]
	Validation     console.TaskValidation
}

func NewOutput(option *cfg.Option, stdout, stderr io.Writer) *Output {
	if stdout == nil {
		stdout = os.Stdout
	}
	if stderr == nil {
		stderr = os.Stderr
	}
	return &Output{option: option, stdout: stdout, stderr: stderr}
}

// Stdout transfers presentation to a direct command with its own output schema.
func (o *Output) Stdout() io.Writer {
	o.started = true
	return o.stdout
}

// Open selects the destination once, before execution. JSONL recordings are
// new files; resuming must never truncate the source history.
func (o *Output) Open() error {
	if err := cfg.ResolveOutputFormat(o.option); err != nil {
		return err
	}
	path := o.option.OutputFile
	if path == "" || o.file != nil {
		return nil
	}
	for _, input := range []string{o.option.Resume, o.option.TaskFile, o.option.Prompt} {
		if source, err := os.Stat(input); err == nil {
			if destination, err := os.Stat(path); err == nil && os.SameFile(source, destination) {
				return errors.New("output must differ from input files")
			}
		}
	}
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		return err
	}
	flags := os.O_WRONLY | os.O_CREATE | os.O_TRUNC
	if o.option.OutputFormat == "jsonl" {
		flags = os.O_WRONLY | os.O_CREATE | os.O_EXCL
	}
	file, err := os.OpenFile(path, flags, 0o600)
	if err != nil {
		return err
	}
	o.file, o.stdout = file, file
	return nil
}

// Observe renders profile-wide events for direct scanners and complete JSONL
// recordings. A JSON task result keeps RunTask's session selection instead.
func (o *Output) Observe(stream interface {
	Observe(func(*aop.Event)) *eventbus.Subscription[*aop.Event]
}) {
	if o.renderer != nil || (o.option.OutputFormat == "" || o.option.OutputFormat == "text") {
		return
	}
	o.started = true
	o.renderer = outputpkg.NewEventWriter(o.stdout, o.option.OutputFormat)
	o.sub = stream.Observe(o.renderer.HandleEvent)
}

func (o *Output) Finish(err error) error {
	if o.sub != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		err = errors.Join(err, o.sub.Close(ctx))
		cancel()
	}
	if o.renderer != nil {
		o.renderer.SetError(err)
		err = errors.Join(err, o.renderer.Close())
	}
	var flagErr *flags.Error
	if err != nil && !o.started && !(errors.As(err, &flagErr) && flagErr.Type == flags.ErrHelp) {
		format := o.option.OutputFormat
		if o.option.JSON {
			format = "json"
		}
		err = errors.Join(err, outputpkg.WriteStartupError(o.stdout, format, err))
	}
	if o.file != nil {
		err = errors.Join(err, o.file.Sync(), o.file.Close())
		o.file = nil
	}
	return err
}

func (o *Output) Run(ctx context.Context, runtime *agentsession.Runtime, sessionID, label, display string, input agentsession.RunInput, finish func(error) error) error {
	o.started = true
	stdout := o.stdout
	if o.renderer != nil {
		stdout = io.Discard
	}
	return console.RunTask(ctx, runtime, o.option, sessionID, label, display, input, finish,
		console.TaskOptions{Stdout: stdout, Stderr: o.stderr, Validation: o.Validation})
}
