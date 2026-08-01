// Package terminal is the single adapter between AIScan's protobuf terminal
// transport and the internal PTY runtime model.
package terminal

import (
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	"github.com/chainreactors/utils/pty"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func FromProto(value *transport.TerminalFrame) pty.Frame {
	if value == nil {
		return pty.Frame{}
	}
	frame := pty.Frame{
		Type: pty.FrameType(value.Type), StreamID: value.StreamId, SessionID: value.SessionId,
		Kind: value.Kind, Name: value.Name, Command: value.Command, Args: value.Args, Data: value.Data,
		Cols: int(value.Cols), Rows: int(value.Rows), Bytes: int(value.Bytes), Offset: value.Offset,
		Singleton: value.Singleton, Error: value.Error, State: pty.State(value.State), ExitCode: int(value.ExitCode),
	}
	frame.Session = InfoFromProto(value.Session)
	for _, session := range value.Sessions {
		if info := InfoFromProto(session); info != nil {
			frame.Sessions = append(frame.Sessions, *info)
		}
	}
	return frame
}

func ToProto(frame pty.Frame) *transport.TerminalFrame {
	value := &transport.TerminalFrame{
		Type: string(frame.Type), StreamId: frame.StreamID, SessionId: frame.SessionID,
		Kind: frame.Kind, Name: frame.Name, Command: frame.Command, Args: frame.Args, Data: frame.Data,
		Cols: int32(frame.Cols), Rows: int32(frame.Rows), Bytes: int32(frame.Bytes), Offset: frame.Offset,
		Singleton: frame.Singleton, Error: frame.Error, State: string(frame.State), ExitCode: int32(frame.ExitCode),
	}
	value.Session = InfoToProto(frame.Session)
	for index := range frame.Sessions {
		value.Sessions = append(value.Sessions, InfoToProto(&frame.Sessions[index]))
	}
	return value
}

func InfoFromProto(value *transport.TerminalInfo) *pty.Info {
	if value == nil {
		return nil
	}
	info := &pty.Info{
		ID: value.Id, Kind: value.Kind, Name: value.Name, Command: value.Command,
		PID: int(value.Pid), ActivitySeq: value.ActivitySeq, OutputBytes: value.OutputBytes,
		ExitCode: int(value.ExitCode), State: pty.State(value.State), KillCause: value.KillCause,
	}
	if value.StartedAt != nil {
		info.StartedAt = value.StartedAt.AsTime()
	}
	if value.LastActivityAt != nil {
		info.LastActivityAt = value.LastActivityAt.AsTime()
	}
	if value.EndedAt != nil {
		info.EndedAt = value.EndedAt.AsTime()
	}
	return info
}

func InfoToProto(value *pty.Info) *transport.TerminalInfo {
	if value == nil {
		return nil
	}
	info := &transport.TerminalInfo{
		Id: value.ID, Kind: value.Kind, Name: value.Name, Command: value.Command,
		Pid: int32(value.PID), ActivitySeq: value.ActivitySeq, OutputBytes: value.OutputBytes,
		ExitCode: int32(value.ExitCode), State: string(value.State), KillCause: value.KillCause,
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

// Marshal emits canonical protobuf JSON for the browser terminal WebSocket.
func Marshal(frame pty.Frame) ([]byte, error) {
	return protojson.Marshal(ToProto(frame))
}

// Unmarshal accepts canonical protobuf JSON from the browser terminal
// WebSocket and returns the runtime PTY frame.
func Unmarshal(data []byte) (pty.Frame, error) {
	value := new(transport.TerminalFrame)
	if err := protojson.Unmarshal(data, value); err != nil {
		return pty.Frame{}, err
	}
	return FromProto(value), nil
}
