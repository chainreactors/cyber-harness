// Package extensions contains AIScan-owned typed AOP extension helpers.
//
// Stable AOP payloads live in the root aop package. Product-specific metadata
// is namespaced here so runtime and transport packages do not need handwritten
// JSON envelopes or duplicate DTOs.
package extensions

import (
	"github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
)

const (
	CommandNamespace    = "command"
	CompactNamespace    = "compact"
	DelegationNamespace = "delegation"
	EvalNamespace       = "eval"
	WebNamespace        = "io.chainreactors.aiscan.web"
)

const (
	CompactStateStart = "compact_start"
	CompactStateEnd   = "compact_end"
	CompactStateError = "compact_error"

	EvalStateStart = "eval_start"
	EvalStateEnd   = "eval_end"
	EvalStateError = "eval_error"
)

const (
	DelegationContextFork   = "fork"
	DelegationContextFresh  = "fresh"
	DelegationRunBackground = "background"
	DelegationRunForeground = "foreground"
)

type CommandDetail = transport.CommandDetail
type CompactDetail = transport.CompactDetail
type DelegationDetail = transport.DelegationDetail
type EvalControl = transport.EvalControl
type EvalDetail = transport.EvalDetail
type WebMessageExtension = transport.WebMessageExtension

func GetCommandDetail(event *aop.Event) (CommandDetail, bool, error) {
	value := new(CommandDetail)
	ok, err := aop.ProtoExtension(event, CommandNamespace, value)
	return *value, ok, err
}

func SetCommandDetail(event *aop.Event, value CommandDetail) error {
	return aop.SetProtoExtension(event, CommandNamespace, &value)
}

func GetCompactDetail(event *aop.Event) (CompactDetail, bool, error) {
	value := new(CompactDetail)
	ok, err := aop.ProtoExtension(event, CompactNamespace, value)
	return *value, ok, err
}

func SetCompactDetail(event *aop.Event, value CompactDetail) error {
	return aop.SetProtoExtension(event, CompactNamespace, &value)
}

func GetDelegation(event *aop.Event) (DelegationDetail, bool, error) {
	value := new(DelegationDetail)
	ok, err := aop.ProtoExtension(event, DelegationNamespace, value)
	return *value, ok, err
}

func SetDelegation(event *aop.Event, value DelegationDetail) error {
	return aop.SetProtoExtension(event, DelegationNamespace, &value)
}

func GetEvalControl(event *aop.Event) (EvalControl, bool, error) {
	value := new(EvalControl)
	ok, err := aop.ProtoExtension(event, EvalNamespace, value)
	return *value, ok, err
}

func SetEvalControl(event *aop.Event, value EvalControl) error {
	return aop.SetProtoExtension(event, EvalNamespace, &value)
}

func GetEvalDetail(event *aop.Event) (EvalDetail, bool, error) {
	value := new(EvalDetail)
	ok, err := aop.ProtoExtension(event, EvalNamespace, value)
	return *value, ok, err
}

func SetEvalDetail(event *aop.Event, value EvalDetail) error {
	return aop.SetProtoExtension(event, EvalNamespace, &value)
}

func GetWebMessage(event *aop.Event) (WebMessageExtension, bool, error) {
	value := new(WebMessageExtension)
	ok, err := aop.ProtoExtension(event, WebNamespace, value)
	return *value, ok, err
}

func SetWebMessage(event *aop.Event, value WebMessageExtension) error {
	return aop.SetProtoExtension(event, WebNamespace, &value)
}
