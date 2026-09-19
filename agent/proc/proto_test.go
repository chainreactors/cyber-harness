package proc

import (
	"testing"
	"time"

	"github.com/chainreactors/utils/proc"
)

// The wire record must distinguish "this unit has no exit code" from "it
// exited zero". Before Process existed, a subagent or a built-in command was
// reported to every client as pid 0 exiting successfully.
func TestSessionToProtoOmitsProcessFactsForUnitsWithoutAProcess(t *testing.T) {
	started := time.Now()

	withProcess := SessionToProto(&proc.Info{
		ID: "a", Shape: proc.ShapeTTY, State: proc.StateCompleted,
		StartedAt: started, Proc: &proc.Proc{PID: 4242, ExitCode: 3, Signal: "killed"},
	})
	if withProcess.GetShape() != "tty" {
		t.Errorf("shape = %q, want tty", withProcess.GetShape())
	}
	if withProcess.GetProcess() == nil {
		t.Fatal("a terminal unit must report its process")
	}
	if got := withProcess.GetProcess(); got.GetPid() != 4242 || got.GetExitCode() != 3 || got.GetSignal() != "killed" {
		t.Errorf("process = %+v, want pid 4242 exit 3 signal killed", got)
	}
	if withProcess.GetPid() != 4242 || withProcess.GetExitCode() != 3 {
		t.Error("the pre-Process fields must stay populated for existing clients")
	}

	withoutProcess := SessionToProto(&proc.Info{
		ID: "b", Shape: proc.ShapeExtern, State: proc.StateCompleted, StartedAt: started,
	})
	if withoutProcess.GetProcess() != nil {
		t.Errorf("process = %+v, want nil for a unit with no process", withoutProcess.GetProcess())
	}
	if withoutProcess.GetShape() != "extern" {
		t.Errorf("shape = %q, want extern", withoutProcess.GetShape())
	}
}

func TestSessionToProtoCarriesReadinessAndReason(t *testing.T) {
	ready := time.Now()
	info := SessionToProto(&proc.Info{
		ID: "c", Shape: proc.ShapePipe, State: proc.StateFailed,
		StartedAt: ready.Add(-time.Second), ReadyAt: ready, Reason: "not ready: port closed",
	})
	if info.GetReadyAt() == nil || !info.GetReadyAt().AsTime().Equal(ready.UTC()) {
		t.Errorf("ready_at = %v, want %v", info.GetReadyAt(), ready)
	}
	if info.GetKillCause() != "not ready: port closed" {
		t.Errorf("kill_cause = %q, want the unit's reason", info.GetKillCause())
	}
}
