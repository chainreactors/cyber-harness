package provider

import "errors"

var (
	ErrCallTimeout   = errors.New("provider call timeout")
	ErrStreamStalled = errors.New("stream stalled")
)
