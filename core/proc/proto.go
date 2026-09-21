package proc

import (
	ptypb "github.com/chainreactors/cyber/aop/pty"
	"github.com/chainreactors/utils/proc"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// SessionToProto is the single projection from a registry unit onto the wire.
// It lives with the registry rather than with either reporter because the PTY
// protocol and the observe extension publish the same units on the same
// protocol: two copies of this mapping drift the moment a field is added.
func SessionToProto(value *proc.Info) *ptypb.Session {
	if value == nil {
		return nil
	}
	info := &ptypb.Session{
		Id: value.ID, Kind: value.Kind, Name: value.Name, Command: value.Command,
		Shape: string(value.Shape), ActivitySeq: value.ActivitySeq, OutputBytes: value.OutputBytes,
		State: string(value.State), KillCause: value.Reason,
		// Pid and ExitCode stay populated for clients that predate Process.
		Pid: int32(value.ProcessID()), ExitCode: int32(value.ExitStatus()),
	}
	if value.Proc != nil {
		info.Process = &ptypb.Process{
			Pid: int32(value.Proc.PID), ExitCode: int32(value.Proc.ExitCode), Signal: value.Proc.Signal,
		}
	}
	if !value.ReadyAt.IsZero() {
		info.ReadyAt = timestamppb.New(value.ReadyAt)
	}
	if !value.StartedAt.IsZero() {
		info.StartedAt = timestamppb.New(value.StartedAt)
	}
	if !value.LastActivityAt.IsZero() {
		info.LastActivityAt = timestamppb.New(value.LastActivityAt)
	}
	if !value.EndedAt.IsZero() {
		info.EndedAt = timestamppb.New(value.EndedAt)
	}
	return info
}
