package proc

import "time"

// Shape is the mechanism discriminator, fixed when a unit is created. It tells
// a reader which optional detail to expect. Kind is a separate, mutable label
// the caller owns; the two must never be conflated.
type Shape string

const (
	ShapeTTY    Shape = "tty"    // external process on a pseudo-terminal
	ShapePipe   Shape = "pipe"   // external process on plain pipes
	ShapeFunc   Shape = "func"   // in-process function, no OS process
	ShapeExtern Shape = "extern" // work driven by another subsystem
)

type State string

const (
	StateStarting  State = "starting"
	StateRunning   State = "running"
	StateCompleted State = "completed"
	StateKilled    State = "killed"
	StateFailed    State = "failed"
)

// Active reports whether the unit may still do work. Prefer this over
// comparing against StateRunning: a unit waiting on its readiness probe is
// starting, not running, but it is still live.
func (s State) Active() bool { return s == StateStarting || s == StateRunning }

const (
	DefaultTimeout = 30 * time.Minute
	killGrace      = 5 * time.Second
	shutdownGrace  = 2 * time.Second
	// outputDrainGrace lets the output pump catch up with a unit that has just
	// exited, before anything is closed underneath it.
	outputDrainGrace = 100 * time.Millisecond
)

// defaultAbandonGrace bounds the wait for a unit that ignores every stop rung.
// The registry gives up and records the truth rather than blocking forever.
const defaultAbandonGrace = 10 * time.Second

// Info is the wire record for one unit. Every field here is meaningful for
// every shape; anything that is not lives behind an optional pointer.
type Info struct {
	ID      string `json:"id"`
	Shape   Shape  `json:"shape"`
	Kind    string `json:"kind,omitempty"`
	Name    string `json:"name,omitempty"`
	Command string `json:"command"`

	StartedAt      time.Time `json:"started_at"`
	ReadyAt        time.Time `json:"ready_at,omitempty"`
	LastActivityAt time.Time `json:"last_activity_at,omitempty"`
	EndedAt        time.Time `json:"ended_at,omitempty"`

	ActivitySeq int64 `json:"activity_seq,omitempty"`
	OutputBytes int64 `json:"output_bytes,omitempty"`

	State State `json:"state"`
	// Reason records why the unit left Active. It is set for every terminal
	// state, not only for kills.
	Reason string `json:"reason,omitempty"`

	// Proc is non-nil if and only if an OS process backs this unit.
	Proc *Proc `json:"proc,omitempty"`
	// Status is a shell status produced by an in-process interpreter.
	Status *int `json:"status,omitempty"`
}

// ExitStatus is the OS exit code or an in-process shell status when present.
// A nil Proc and nil Status mean the unit has no exit code.
func (i Info) ExitStatus() int {
	if i.Proc == nil {
		if i.Status != nil {
			return *i.Status
		}
		return 0
	}
	return i.Proc.ExitCode
}

// ProcessID is the unit's OS pid, or zero when it has no process.
func (i Info) ProcessID() int {
	if i.Proc == nil {
		return 0
	}
	return i.Proc.PID
}

// Proc carries the facts that exist only for an OS-backed unit.
type Proc struct {
	PID      int    `json:"pid"`
	ExitCode int    `json:"exit_code"`
	Signal   string `json:"signal,omitempty"`
}

// Result is a unit's terminal fact: an OS exit status, interpreter status,
// Go error, or nothing at all.
type Result struct {
	Err      error
	Exited   bool
	ExitCode int
	Signal   string
	Status   *int
}

// Signal is one rung of the stop ladder.
type Signal uint8

const (
	// SigInterrupt asks politely: ETX into a pseudo-terminal, SIGINT or a
	// console break event for a pipe.
	SigInterrupt Signal = iota
	SigTerminate
	SigKill
)

func (s Signal) String() string {
	switch s {
	case SigInterrupt:
		return "interrupt"
	case SigTerminate:
		return "terminate"
	case SigKill:
		return "kill"
	default:
		return "unknown"
	}
}

type EventAction string

const (
	EventSessionCreated EventAction = "created"
	EventSessionUpdated EventAction = "updated"
	EventSessionOutput  EventAction = "output"
	EventSessionClosed  EventAction = "closed"
)

type Event struct {
	Action      EventAction `json:"action"`
	Info        Info        `json:"info"`
	OutputBytes int         `json:"output_bytes,omitempty"`
}
