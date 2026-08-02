// Package api implements AIScan's protocol-neutral Web management API.
//
// Methods consume and return generated protobuf messages. Transport packages
// only adapt envelopes and error codes; Agent execution remains delegated to
// the existing AOP/AgentPool runtime.
package api

import (
	"errors"
	"fmt"

	types "github.com/chainreactors/aiscan/pkg/types"
)

type Code string

const (
	CodeInvalidArgument    Code = "INVALID_ARGUMENT"
	CodeNotFound           Code = "NOT_FOUND"
	CodeAlreadyExists      Code = "ALREADY_EXISTS"
	CodeFailedPrecondition Code = "FAILED_PRECONDITION"
	CodeResourceExhausted  Code = "RESOURCE_EXHAUSTED"
	CodeUnavailable        Code = "UNAVAILABLE"
	CodeInternal           Code = "INTERNAL"
)

type Error struct {
	Code Code
	Err  error
}

func (e *Error) Error() string {
	if e == nil || e.Err == nil {
		return ""
	}
	return e.Err.Error()
}

func (e *Error) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func NewError(code Code, err error) error {
	if err == nil {
		err = errors.New(string(code))
	}
	return &Error{Code: code, Err: err}
}

func Errorf(code Code, format string, args ...any) error {
	return NewError(code, fmt.Errorf(format, args...))
}

func ErrorCode(err error) Code {
	var apiErr *Error
	if errors.As(err, &apiErr) {
		return apiErr.Code
	}
	return CodeInternal
}

type AgentReader interface {
	List() []*types.AgentView
	Count() int
}

type StatusReader interface {
	Status() *types.SystemStatus
}

type API struct {
	Sessions  *Sessions
	Config    *Config
	Scans     *Scans
	SCO       *SCO
	Agents    AgentReader
	Status    StatusReader
	ServerURL string
}

func (a *API) ListAgents(*types.ListAgentsRequest) *types.ListAgentsResponse {
	response := &types.ListAgentsResponse{}
	if a != nil && a.Agents != nil {
		response.Agents = a.Agents.List()
	}
	return response
}

func (a *API) GetStatus(*types.GetStatusRequest) *types.GetStatusResponse {
	response := &types.GetStatusResponse{Status: &types.SystemStatus{}}
	if a == nil {
		return response
	}
	if a.Status != nil {
		response.Status = a.Status.Status()
	}
	if response.Status == nil {
		response.Status = &types.SystemStatus{}
	}
	if a.Agents != nil {
		response.Status.Agents = uint32(a.Agents.Count())
	}
	if a.ServerURL != "" {
		response.Status.ServerUrl = a.ServerURL
	}
	return response
}
