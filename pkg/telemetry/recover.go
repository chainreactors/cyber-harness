package telemetry

import (
	"fmt"
	"runtime/debug"

	"github.com/chainreactors/logs"
)

// SafeGo launches a goroutine with automatic panic recovery.
// On panic the stack is logged and the goroutine exits cleanly.
func SafeGo(name string, fn func()) {
	go func() {
		defer SDKGoRecover(name)
		fn()
	}()
}

// SafeRun executes fn synchronously with panic recovery.
// On panic the stack is logged and SafeRun returns normally,
// so the caller (e.g. a worker loop) can continue processing.
func SafeRun(name string, fn func()) {
	defer func() {
		if r := recover(); r != nil {
			logs.Log.Errorf("[%s] panic recovered: %v\n%s", name, r, debug.Stack())
		}
	}()
	fn()
}

// RecoverAsError is designed for tool Execute methods. Call as:
//
//	defer telemetry.RecoverAsError("toolname", &err)
//
// It converts a panic into a returned error so the process stays alive.
func RecoverAsError(name string, errp *error) {
	if r := recover(); r != nil {
		stack := debug.Stack()
		logs.Log.Errorf("[%s] panic recovered: %v\n%s", name, r, stack)
		*errp = fmt.Errorf("[%s] panic: %v", name, r)
	}
}

// SDKGoRecover recovers from a panic inside a goroutine that processes SDK
// results. It logs the panic; the deferred close(out) in the caller signals
// the consumer that the stream ended.
func SDKGoRecover(engine string) {
	r := recover()
	if r == nil {
		return
	}
	stack := debug.Stack()
	logs.Log.Errorf("[sdk.%s] goroutine panic recovered: %v\n%s", engine, r, stack)
}
