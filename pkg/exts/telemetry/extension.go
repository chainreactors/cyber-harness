// Package telemetry provides the profile telemetry extension. It persists the
// canonical AOP event stream and may later host metrics/tracing consumers.
//
// The implementation is kept in eventoutput for the moment; this package is
// the public extension boundary and makes the ownership/name explicit.
package telemetry

import (
	coreevents "github.com/chainreactors/aiscan/core/events"
	legacy "github.com/chainreactors/aiscan/pkg/exts/eventoutput"
)

type Options = legacy.Options
type Extension = legacy.Extension

func New(events *coreevents.Stream, options Options) (*Extension, error) {
	return legacy.New(events, options)
}
