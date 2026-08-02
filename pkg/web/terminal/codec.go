// Package terminal adapts the AOP PTY extension to the internal PTY runtime.
package terminal

import (
	ptyproto "github.com/chainreactors/aiscan/aop/pty"
	"github.com/chainreactors/utils/pty"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func FromProto(value *ptyproto.ProtocolMessage) pty.Frame {
	if value == nil {
		return pty.Frame{}
	}
	switch payload := value.Message.(type) {
	case *ptyproto.ProtocolMessage_Open:
		v := payload.Open
		return pty.Frame{Type: pty.FrameOpen, StreamID: v.StreamId, Kind: v.Kind, Name: v.Name, Command: v.Command, Args: v.Args, Cols: int(v.Cols), Rows: int(v.Rows), Singleton: v.Singleton}
	case *ptyproto.ProtocolMessage_Opened:
		return pty.Frame{Type: pty.FrameOpened, StreamID: payload.Opened.StreamId, Session: InfoFromProto(payload.Opened.Session)}
	case *ptyproto.ProtocolMessage_Input:
		return pty.Frame{Type: pty.FrameInput, StreamID: payload.Input.StreamId, Data: payload.Input.Data}
	case *ptyproto.ProtocolMessage_Output:
		return pty.Frame{Type: pty.FrameOutput, StreamID: payload.Output.StreamId, Data: payload.Output.Data, Offset: payload.Output.Offset}
	case *ptyproto.ProtocolMessage_Resize:
		return pty.Frame{Type: pty.FrameResize, StreamID: payload.Resize.StreamId, Cols: int(payload.Resize.Cols), Rows: int(payload.Resize.Rows)}
	case *ptyproto.ProtocolMessage_List:
		return pty.Frame{Type: pty.FrameList, StreamID: payload.List.StreamId}
	case *ptyproto.ProtocolMessage_Sessions:
		frame := pty.Frame{Type: pty.FrameSessions, StreamID: payload.Sessions.StreamId}
		for _, session := range payload.Sessions.Sessions {
			if info := InfoFromProto(session); info != nil {
				frame.Sessions = append(frame.Sessions, *info)
			}
		}
		return frame
	case *ptyproto.ProtocolMessage_Attach:
		v := payload.Attach
		return pty.Frame{Type: pty.FrameAttach, StreamID: v.StreamId, SessionID: v.SessionId, Cols: int(v.Cols), Rows: int(v.Rows)}
	case *ptyproto.ProtocolMessage_Attached:
		return pty.Frame{Type: pty.FrameAttached, StreamID: payload.Attached.StreamId, Session: InfoFromProto(payload.Attached.Session)}
	case *ptyproto.ProtocolMessage_Detach:
		return pty.Frame{Type: pty.FrameDetach, StreamID: payload.Detach.StreamId}
	case *ptyproto.ProtocolMessage_Detached:
		return pty.Frame{Type: pty.FrameDetached, StreamID: payload.Detached.StreamId}
	case *ptyproto.ProtocolMessage_Kill:
		return pty.Frame{Type: pty.FrameKill, StreamID: payload.Kill.StreamId}
	case *ptyproto.ProtocolMessage_Close:
		return pty.Frame{Type: pty.FrameKill, StreamID: payload.Close.StreamId}
	case *ptyproto.ProtocolMessage_Closed:
		return pty.Frame{Type: pty.FrameClosed, StreamID: payload.Closed.StreamId, Session: InfoFromProto(payload.Closed.Session)}
	case *ptyproto.ProtocolMessage_State:
		return pty.Frame{Type: pty.FrameOpened, StreamID: payload.State.StreamId, Session: InfoFromProto(payload.State.Session)}
	case *ptyproto.ProtocolMessage_Error:
		return pty.Frame{Type: pty.FrameError, StreamID: payload.Error.StreamId, Error: payload.Error.Message}
	default:
		return pty.Frame{}
	}
}

func ToProto(frame pty.Frame) *ptyproto.ProtocolMessage {
	message := &ptyproto.ProtocolMessage{}
	switch frame.Type {
	case pty.FrameOpen:
		message.Message = &ptyproto.ProtocolMessage_Open{Open: &ptyproto.Open{StreamId: frame.StreamID, Kind: frame.Kind, Name: frame.Name, Command: frame.Command, Args: frame.Args, Cols: int32(frame.Cols), Rows: int32(frame.Rows), Singleton: frame.Singleton}}
	case pty.FrameOpened:
		message.Message = &ptyproto.ProtocolMessage_Opened{Opened: &ptyproto.Opened{StreamId: frame.StreamID, Session: InfoToProto(frame.Session)}}
	case pty.FrameInput:
		message.Message = &ptyproto.ProtocolMessage_Input{Input: &ptyproto.Input{StreamId: frame.StreamID, Data: frame.Data}}
	case pty.FrameOutput:
		message.Message = &ptyproto.ProtocolMessage_Output{Output: &ptyproto.Output{StreamId: frame.StreamID, Data: frame.Data, Offset: frame.Offset}}
	case pty.FrameResize:
		message.Message = &ptyproto.ProtocolMessage_Resize{Resize: &ptyproto.Resize{StreamId: frame.StreamID, Cols: int32(frame.Cols), Rows: int32(frame.Rows)}}
	case pty.FrameList:
		message.Message = &ptyproto.ProtocolMessage_List{List: &ptyproto.List{StreamId: frame.StreamID}}
	case pty.FrameSessions:
		value := &ptyproto.Sessions{StreamId: frame.StreamID}
		for index := range frame.Sessions {
			value.Sessions = append(value.Sessions, InfoToProto(&frame.Sessions[index]))
		}
		message.Message = &ptyproto.ProtocolMessage_Sessions{Sessions: value}
	case pty.FrameAttach:
		message.Message = &ptyproto.ProtocolMessage_Attach{Attach: &ptyproto.Attach{StreamId: frame.StreamID, SessionId: frame.SessionID, Cols: int32(frame.Cols), Rows: int32(frame.Rows)}}
	case pty.FrameAttached:
		message.Message = &ptyproto.ProtocolMessage_Attached{Attached: &ptyproto.Attached{StreamId: frame.StreamID, Session: InfoToProto(frame.Session)}}
	case pty.FrameDetach:
		message.Message = &ptyproto.ProtocolMessage_Detach{Detach: &ptyproto.Detach{StreamId: frame.StreamID}}
	case pty.FrameDetached:
		message.Message = &ptyproto.ProtocolMessage_Detached{Detached: &ptyproto.Detached{StreamId: frame.StreamID}}
	case pty.FrameKill:
		message.Message = &ptyproto.ProtocolMessage_Kill{Kill: &ptyproto.Kill{StreamId: frame.StreamID}}
	case pty.FrameClosed:
		message.Message = &ptyproto.ProtocolMessage_Closed{Closed: &ptyproto.Closed{StreamId: frame.StreamID, Session: InfoToProto(frame.Session)}}
	case pty.FrameError:
		message.Message = &ptyproto.ProtocolMessage_Error{Error: &ptyproto.Error{StreamId: frame.StreamID, Message: frame.Error}}
	default:
		message.Message = &ptyproto.ProtocolMessage_Error{Error: &ptyproto.Error{StreamId: frame.StreamID, Message: "unsupported PTY frame"}}
	}
	return message
}

func InfoFromProto(value *ptyproto.Session) *pty.Info {
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

func InfoToProto(value *pty.Info) *ptyproto.Session {
	if value == nil {
		return nil
	}
	info := &ptyproto.Session{
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
