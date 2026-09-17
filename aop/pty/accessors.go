package pty

// StreamID returns the stream identity carried by a canonical PTY message.
// Every variant that participates in a stream publishes stream_id, but the
// protobuf oneof wrapper types expose no shared accessor, so this switch is the
// only place that has to know the envelope layout.
func StreamID(message *ProtocolMessage) string {
	if message == nil {
		return ""
	}
	switch payload := message.Message.(type) {
	case *ProtocolMessage_Open:
		return payload.Open.GetStreamId()
	case *ProtocolMessage_Opened:
		return payload.Opened.GetStreamId()
	case *ProtocolMessage_Input:
		return payload.Input.GetStreamId()
	case *ProtocolMessage_Output:
		return payload.Output.GetStreamId()
	case *ProtocolMessage_Resize:
		return payload.Resize.GetStreamId()
	case *ProtocolMessage_List:
		return payload.List.GetStreamId()
	case *ProtocolMessage_Sessions:
		return payload.Sessions.GetStreamId()
	case *ProtocolMessage_Attach:
		return payload.Attach.GetStreamId()
	case *ProtocolMessage_Attached:
		return payload.Attached.GetStreamId()
	case *ProtocolMessage_Detach:
		return payload.Detach.GetStreamId()
	case *ProtocolMessage_Detached:
		return payload.Detached.GetStreamId()
	case *ProtocolMessage_Kill:
		return payload.Kill.GetStreamId()
	case *ProtocolMessage_Close:
		return payload.Close.GetStreamId()
	case *ProtocolMessage_Closed:
		return payload.Closed.GetStreamId()
	case *ProtocolMessage_State:
		return payload.State.GetStreamId()
	case *ProtocolMessage_Error:
		return payload.Error.GetStreamId()
	default:
		return ""
	}
}

// NodeID returns the node that owns the stream for the messages that may
// address a node directly instead of an already-routed stream.
func NodeID(message *ProtocolMessage) string {
	if message == nil {
		return ""
	}
	switch payload := message.Message.(type) {
	case *ProtocolMessage_Open:
		return payload.Open.GetNodeId()
	case *ProtocolMessage_List:
		return payload.List.GetNodeId()
	default:
		return ""
	}
}

func IsDetach(message *ProtocolMessage) bool {
	_, ok := message.GetMessage().(*ProtocolMessage_Detach)
	return ok
}

func IsClosed(message *ProtocolMessage) bool {
	_, ok := message.GetMessage().(*ProtocolMessage_Closed)
	return ok
}

func NewList(streamID, nodeID string) *ProtocolMessage {
	return &ProtocolMessage{Message: &ProtocolMessage_List{List: &List{StreamId: streamID, NodeId: nodeID}}}
}

func NewKill(streamID string) *ProtocolMessage {
	return &ProtocolMessage{Message: &ProtocolMessage_Kill{Kill: &Kill{StreamId: streamID}}}
}

func NewDetach(streamID string) *ProtocolMessage {
	return &ProtocolMessage{Message: &ProtocolMessage_Detach{Detach: &Detach{StreamId: streamID}}}
}

func NewDetached(streamID string) *ProtocolMessage {
	return &ProtocolMessage{Message: &ProtocolMessage_Detached{Detached: &Detached{StreamId: streamID}}}
}
