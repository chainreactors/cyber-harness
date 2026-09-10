package aop

import (
	"strconv"
	"sync/atomic"
	"time"

	"google.golang.org/protobuf/proto"
)

var envelopeSequence atomic.Uint64

func EnvelopeID() string {
	return "runtime:" + strconv.FormatInt(time.Now().UnixNano(), 36) + ":" + strconv.FormatUint(envelopeSequence.Add(1), 36)
}
func Reply(replyTo string, message proto.Message) *Envelope {
	return MustWrap(EnvelopeID(), replyTo, message)
}
func NewProtocolError(code, message string) *ProtocolMessage {
	return &ProtocolMessage{Message: &ProtocolMessage_ProtocolError{ProtocolError: &ProtocolError{Code: code, Message: message}}}
}
