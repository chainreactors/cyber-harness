package proc

import (
	"context"
	"io"
	"os"
	"strings"
	"time"
)

// Attachment starts one unit of work. The tty, pipe and func shapes are
// implemented in this package; a consumer in another module implements its own
// by closing over whatever drives the work.
type Attachment interface {
	Start(ctx context.Context) (*Attached, error)
}

// AttachFunc adapts a function to Attachment. It is the whole extension point:
// a consumer never has to declare a type.
type AttachFunc func(ctx context.Context) (*Attached, error)

func (f AttachFunc) Start(ctx context.Context) (*Attached, error) { return f(ctx) }

// Attached is what a started attachment hands back to the registry.
//
// Only Wait is required. Every other field is a capability, and a nil field
// means this shape does not have it -- so no shape ever implements a method
// that lies. The registry checks for nil once, in one place, instead of every
// call site type-asserting.
type Attached struct {
	// Shape is the mechanism this attachment implements. The attachment is the
	// only thing that knows it, so the attachment declares it.
	Shape Shape

	// Wait blocks until the unit reaches a terminal condition. Required.
	Wait func() Result

	// Signal escalates a stop request. Nil means the unit can only be stopped
	// by cancelling the context it was started with, which the registry always
	// does first.
	Signal func(Signal) error

	// Output is the diagnostic stream. The registry pumps it into the single
	// ring buffer. Nil means this unit produces no byte output.
	Output io.Reader

	// Raw is a byte-exact stream the registry never reads, buffers or inspects.
	// It is claimed by exactly one owner through Manager.Raw. A protocol stream
	// belongs here and never in the ring buffer, which is lossy by design.
	Raw io.ReadCloser

	// Input accepts bytes written through Manager.Write. Nil means the unit
	// takes no input.
	Input io.WriteCloser

	// Resize is nil for anything that is not a terminal.
	Resize func(cols, rows int) error

	// Ready reports when the unit is usable. Nil means "ready once started".
	// The registry never learns what readiness means for this unit.
	Ready func(ctx context.Context) error

	// Live is polled while the unit runs. A non-nil error stops it.
	Live func(ctx context.Context) error

	// Proc is non-nil if and only if an OS process backs this unit.
	Proc *Proc

	// Close releases the attachment's resources. The registry calls it after
	// Wait returns and before draining the output pumps -- that order is what
	// makes a blocked Read return.
	Close func() error
}

// Spec carries the registry's concerns. Everything shape-specific lives in the
// Attachment. Together they replace the old Create/CreateRaw/CreateCmd/
// CreateCmdRaw/CreateFunc/CreateInteractiveFunc family.
type Spec struct {
	Name string
	Kind string
	// Command is the human-facing label for the unit.
	Command string
	// Timeout bounds the unit's lifetime. Zero means no registry deadline, so
	// the unit lives until it ends, until it is stopped, or until the context
	// it was started with is done. A registry of long-lived units has to be
	// able to say that.
	Timeout time.Duration
	// BufferCap overrides the manager's ring buffer size.
	BufferCap int
	// OutputFile mirrors the diagnostic stream to a file.
	OutputFile string
	// StripANSI removes terminal control bytes from the ring buffer. It never
	// affects Raw.
	StripANSI bool
	// LiveEvery is the liveness poll interval; it is ignored when the
	// attachment carries no Live probe.
	LiveEvery time.Duration
}

// EnvMode selects how ProcOptions.Env combines with the current environment.
type EnvMode uint8

const (
	// EnvInherit merges Env over os.Environ().
	EnvInherit EnvMode = iota
	// EnvReplace uses Env verbatim. A nil or empty Env means an empty
	// environment, which EnvInherit cannot express.
	EnvReplace
)

// ProcOptions describes an OS process for the tty and pipe shapes.
type ProcOptions struct {
	// Binary and Args name the program directly. Line is the alternative: a
	// command line run through the platform shell.
	Binary string
	Args   []string
	Line   string

	Dir     string
	Env     []string
	EnvMode EnvMode

	// RawStdout diverts stdout to Attached.Raw byte-for-byte, leaving only
	// stderr in the ring buffer. Pipe only; a pseudo-terminal has one stream.
	RawStdout bool

	// Ready and Live are supplied by the caller and passed through untouched.
	Ready func(ctx context.Context) error
	Live  func(ctx context.Context) error
}

func (o ProcOptions) label() string {
	if o.Line != "" {
		return o.Line
	}
	if len(o.Args) == 0 {
		return o.Binary
	}
	return o.Binary + " " + strings.Join(o.Args, " ")
}

// environ resolves the process environment according to EnvMode. It returns
// nil only for EnvInherit with no overrides, which means "inherit everything".
func (o ProcOptions) environ() []string {
	switch o.EnvMode {
	case EnvReplace:
		if o.Env == nil {
			return []string{}
		}
		return append([]string(nil), o.Env...)
	default:
		if len(o.Env) == 0 {
			return nil
		}
		return mergeEnv(os.Environ(), o.Env)
	}
}

// mergeEnv merges override env vars into base, replacing any existing keys.
func mergeEnv(base, override []string) []string {
	overrideKeys := make(map[string]bool, len(override))
	for _, e := range override {
		if k, _, ok := strings.Cut(e, "="); ok {
			overrideKeys[k] = true
		}
	}
	result := make([]string, 0, len(base)+len(override))
	for _, e := range base {
		if k, _, ok := strings.Cut(e, "="); ok && overrideKeys[k] {
			continue
		}
		result = append(result, e)
	}
	return append(result, override...)
}

// StopOptions tunes one stop request.
type StopOptions struct {
	// From is the first rung of the ladder. The zero value starts at
	// SigInterrupt.
	From Signal
	// Grace is the wait before escalating to the next rung.
	Grace time.Duration
	// Reason is recorded on the unit.
	Reason string
}
