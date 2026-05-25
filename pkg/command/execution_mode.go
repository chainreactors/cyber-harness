package command

type ExecutionMode int

const (
	ExecSequential ExecutionMode = iota
	ExecParallel
)

// ParallelSafe is an optional interface for tools that declare whether
// they can execute concurrently with other tools in the same batch.
// Tools that do not implement this default to ExecSequential.
type ParallelSafe interface {
	ExecutionMode() ExecutionMode
}
