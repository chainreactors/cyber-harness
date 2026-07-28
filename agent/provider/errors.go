package provider

import "errors"

var (
	ErrCallTimeout      = errors.New("provider call timeout")
	ErrStreamStalled    = errors.New("stream stalled")
	ErrStreamIncomplete = errors.New("stream ended before terminal marker")
)
