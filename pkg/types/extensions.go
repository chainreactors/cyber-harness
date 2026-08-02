// Package types contains AIScan-owned protobuf messages and typed AOP extension helpers.
//
// Stable AOP payloads live in the root aop package. Product-specific metadata
// is carried as typed Any values owned by AIScan protobuf packages.
package types

import (
	"github.com/chainreactors/aiscan/aop"
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

func GetWebMessage(event *aop.Event) (WebMessageMetadata, bool, error) {
	value := new(WebMessageMetadata)
	ok, err := aop.FindTypedExtension(event, value)
	return *value, ok, err
}

func SetWebMessage(event *aop.Event, value WebMessageMetadata) error {
	return aop.SetTypedExtension(event, &value)
}
