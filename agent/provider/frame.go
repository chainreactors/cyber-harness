package provider

import "context"

type RawFrame struct {
	Provider  string
	Protocol  string
	EventType string
	Direction string
	Transport string
	Payload   []byte
	MediaType string
}

type frameObserverKey struct{}

func WithFrameObserver(ctx context.Context, observer func(RawFrame)) context.Context {
	if observer == nil {
		return ctx
	}
	return context.WithValue(ctx, frameObserverKey{}, observer)
}

func captureFrame(ctx context.Context, frame RawFrame) {
	observer, _ := ctx.Value(frameObserverKey{}).(func(RawFrame))
	if observer == nil {
		return
	}
	frame.Payload = append([]byte(nil), frame.Payload...)
	observer(frame)
}
