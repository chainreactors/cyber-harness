package aop

import (
	"fmt"

	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/anypb"
)

func Wrap(id, replyTo string, message proto.Message) (*Envelope, error) {
	if message == nil {
		return nil, fmt.Errorf("AOP payload is required")
	}
	payload, err := anypb.New(message)
	if err != nil {
		return nil, err
	}
	return &Envelope{Id: id, ReplyTo: replyTo, Payload: payload}, nil
}

func MustWrap(id, replyTo string, message proto.Message) *Envelope {
	envelope, err := Wrap(id, replyTo, message)
	if err != nil {
		panic(err)
	}
	return envelope
}

func Unwrap(envelope *Envelope) (proto.Message, error) {
	if envelope == nil || envelope.Payload == nil {
		return nil, fmt.Errorf("AOP envelope payload is required")
	}
	message, err := envelope.Payload.UnmarshalNew()
	if err != nil {
		return nil, fmt.Errorf("decode %s: %w", envelope.Payload.TypeUrl, err)
	}
	return message, nil
}
