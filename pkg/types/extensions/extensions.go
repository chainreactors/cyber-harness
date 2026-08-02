// Package extensions contains AIScan-owned typed AOP extension helpers.
//
// Stable AOP payloads live in the root aop package. Product-specific metadata
// is carried as typed Any values owned by AIScan protobuf packages.
package extensions

import (
	"github.com/chainreactors/aiscan/aop"
	agentpb "github.com/chainreactors/aiscan/pkg/types/agent"
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

type CommandDetail = agentpb.CommandDetail
type CompactDetail = agentpb.CompactDetail
type DelegationDetail = agentpb.DelegationDetail
type EvalControl = agentpb.EvalControl
type EvalDetail = agentpb.EvalDetail
type WebMessageExtension = agentpb.WebMessageMetadata

func GetCommandDetail(event *aop.Event) (CommandDetail, bool, error) {
	value := new(CommandDetail)
	ok, err := aop.FindTypedExtension(event, value)
	return *value, ok, err
}

func SetCommandDetail(event *aop.Event, value CommandDetail) error {
	return aop.SetTypedExtension(event, &value)
}

func GetCompactDetail(event *aop.Event) (CompactDetail, bool, error) {
	value := new(CompactDetail)
	ok, err := aop.FindTypedExtension(event, value)
	return *value, ok, err
}

func SetCompactDetail(event *aop.Event, value CompactDetail) error {
	return aop.SetTypedExtension(event, &value)
}

func GetDelegation(event *aop.Event) (DelegationDetail, bool, error) {
	value := new(DelegationDetail)
	ok, err := aop.FindTypedExtension(event, value)
	return *value, ok, err
}

func SetDelegation(event *aop.Event, value DelegationDetail) error {
	return aop.SetTypedExtension(event, &value)
}

func GetEvalControl(event *aop.Event) (EvalControl, bool, error) {
	value := new(EvalControl)
	ok, err := aop.FindTypedExtension(event, value)
	return *value, ok, err
}

func SetEvalControl(event *aop.Event, value EvalControl) error {
	return aop.SetTypedExtension(event, &value)
}

func GetEvalDetail(event *aop.Event) (EvalDetail, bool, error) {
	value := new(EvalDetail)
	ok, err := aop.FindTypedExtension(event, value)
	return *value, ok, err
}

func SetEvalDetail(event *aop.Event, value EvalDetail) error {
	return aop.SetTypedExtension(event, &value)
}

func GetWebMessage(event *aop.Event) (WebMessageExtension, bool, error) {
	value := new(WebMessageExtension)
	ok, err := aop.FindTypedExtension(event, value)
	return *value, ok, err
}

func SetWebMessage(event *aop.Event, value WebMessageExtension) error {
	return aop.SetTypedExtension(event, &value)
}
