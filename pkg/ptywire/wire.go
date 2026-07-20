package ptywire

import (
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/utils/pty"
)

type Message struct {
	Type     string          `json:"type"`
	StreamID string          `json:"stream_id,omitempty"`
	Data     string          `json:"data,omitempty"`
	DataB64  string          `json:"data_b64,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

type Payload struct {
	SessionID string      `json:"session_id,omitempty"`
	Data      string      `json:"data,omitempty"`
	DataB64   string      `json:"data_b64,omitempty"`
	Command   string      `json:"command,omitempty"`
	Kind      string      `json:"kind,omitempty"`
	Args      []string    `json:"args,omitempty"`
	Name      string      `json:"name,omitempty"`
	Rows      int         `json:"rows,omitempty"`
	Cols      int         `json:"cols,omitempty"`
	Bytes     int         `json:"bytes,omitempty"`
	Singleton bool        `json:"singleton,omitempty"`
	State     tmux.State  `json:"state,omitempty"`
	ExitCode  int         `json:"exit_code,omitempty"`
	Session   *tmux.Info  `json:"session,omitempty"`
	Sessions  []tmux.Info `json:"sessions,omitempty"`
}

func ToFrame(msg Message) (pty.Frame, error) {
	frameType, ok := frameTypeFromMessage(msg.Type)
	if !ok {
		return pty.Frame{}, fmt.Errorf("unsupported pty message: %s", msg.Type)
	}
	payload, err := DecodePayload(msg.Payload)
	if err != nil {
		return pty.Frame{}, err
	}
	data, err := decodeData(payload.Data, payload.DataB64)
	if err != nil {
		return pty.Frame{}, err
	}
	if len(data) == 0 {
		data, err = decodeData(msg.Data, msg.DataB64)
		if err != nil {
			return pty.Frame{}, err
		}
	}
	frame := pty.Frame{
		Type: frameType, StreamID: msg.StreamID, SessionID: payload.SessionID,
		Kind: payload.Kind, Name: payload.Name, Command: payload.Command,
		Args: append([]string(nil), payload.Args...), Data: data,
		Cols: payload.Cols, Rows: payload.Rows, Bytes: payload.Bytes,
		Singleton: payload.Singleton, State: payload.State, ExitCode: payload.ExitCode,
		Session: payload.Session, Sessions: append([]tmux.Info(nil), payload.Sessions...),
	}
	if frame.SessionID == "" && payload.Session != nil {
		frame.SessionID = payload.Session.ID
	}
	if frame.Kind == "" && payload.Session != nil {
		frame.Kind = payload.Session.Kind
	}
	if frame.Name == "" && payload.Session != nil {
		frame.Name = payload.Session.Name
	}
	return frame, nil
}

func FromFrame(frame pty.Frame) Message {
	msg := Message{Type: messageTypeFromFrame(frame.Type), StreamID: frame.StreamID}
	switch frame.Type {
	case pty.FrameOpen, pty.FrameAttach, pty.FrameInput, pty.FrameResize,
		pty.FrameDetach, pty.FrameKill, pty.FrameList:
		payload := Payload{
			SessionID: frame.SessionID, Command: frame.Command, Kind: frame.Kind,
			Args: append([]string(nil), frame.Args...), Name: frame.Name,
			Rows: frame.Rows, Cols: frame.Cols, Bytes: frame.Bytes, Singleton: frame.Singleton,
		}
		encodePayloadData(&payload, frame.Data)
		msg.Payload = MustJSON(payload)
	case pty.FrameOutput:
		if frame.SessionID != "" {
			msg.Payload = MustJSON(map[string]any{"session_id": frame.SessionID})
		}
		encodeMessageData(&msg, frame.Data)
	case pty.FrameError:
		if frame.Error != "" {
			msg.Data = frame.Error
		} else {
			msg.Data = string(frame.Data)
		}
	case pty.FrameOpened:
		msg.Payload = MustJSON(map[string]any{
			"session_id": frame.SessionID, "kind": frame.Kind, "name": frame.Name,
			"pid": sessionPID(frame), "session": frame.Session,
		})
	case pty.FrameAttached:
		msg.Payload = MustJSON(map[string]any{"session_id": frame.SessionID, "session": frame.Session})
	case pty.FrameDetached:
		msg.Payload = MustJSON(map[string]any{"session_id": frame.SessionID})
	case pty.FrameSessions:
		msg.Payload = MustJSON(map[string]any{"sessions": frame.Sessions})
	case pty.FrameClosed:
		msg.Payload = MustJSON(map[string]any{
			"session_id": frame.SessionID, "state": frame.State,
			"exit_code": frame.ExitCode, "session": frame.Session,
		})
	}
	return msg
}

func DecodePayload(raw json.RawMessage) (Payload, error) {
	var payload Payload
	if len(raw) > 0 {
		if err := json.Unmarshal(raw, &payload); err != nil {
			return payload, fmt.Errorf("decode pty payload: %w", err)
		}
	}
	return payload, nil
}

func MustJSON(value any) json.RawMessage {
	data, _ := json.Marshal(value)
	return data
}

func messageTypeFromFrame(frameType pty.FrameType) string {
	if frameType == "" {
		return ""
	}
	return "pty." + string(frameType)
}

var frameTypes = map[string]pty.FrameType{
	string(pty.FrameOpen): pty.FrameOpen, string(pty.FrameOpened): pty.FrameOpened,
	string(pty.FrameAttach): pty.FrameAttach, string(pty.FrameAttached): pty.FrameAttached,
	string(pty.FrameInput): pty.FrameInput, string(pty.FrameOutput): pty.FrameOutput,
	string(pty.FrameResize): pty.FrameResize, string(pty.FrameDetach): pty.FrameDetach,
	string(pty.FrameDetached): pty.FrameDetached, string(pty.FrameKill): pty.FrameKill,
	string(pty.FrameList): pty.FrameList, string(pty.FrameSessions): pty.FrameSessions,
	string(pty.FrameClosed): pty.FrameClosed, string(pty.FrameError): pty.FrameError,
}

func frameTypeFromMessage(messageType string) (pty.FrameType, bool) {
	if !strings.HasPrefix(messageType, "pty.") {
		return "", false
	}
	frameType, ok := frameTypes[strings.TrimPrefix(messageType, "pty.")]
	return frameType, ok
}

func decodeData(text, encoded string) ([]byte, error) {
	if encoded != "" {
		data, err := base64.StdEncoding.DecodeString(encoded)
		if err != nil {
			return nil, fmt.Errorf("decode terminal data: %w", err)
		}
		return data, nil
	}
	return []byte(text), nil
}

func encodeMessageData(msg *Message, data []byte) {
	if len(data) == 0 {
		return
	}
	if utf8.Valid(data) {
		msg.Data = string(data)
		return
	}
	msg.DataB64 = base64.StdEncoding.EncodeToString(data)
}

func encodePayloadData(payload *Payload, data []byte) {
	if len(data) == 0 {
		return
	}
	if utf8.Valid(data) {
		payload.Data = string(data)
		return
	}
	payload.DataB64 = base64.StdEncoding.EncodeToString(data)
}

func sessionPID(frame pty.Frame) int {
	if frame.Session == nil {
		return 0
	}
	return frame.Session.PID
}
