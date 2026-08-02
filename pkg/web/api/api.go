// Package api implements AIScan's protocol-neutral Web management API.
//
// Methods consume and return generated protobuf messages. Transport packages
// only adapt envelopes and error codes; Agent execution remains delegated to
// the existing AOP/AgentPool runtime.
package api

import (
	"context"
	"errors"
	"fmt"

	aop "github.com/chainreactors/aiscan/aop"
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

type SessionService interface {
	ListSessions(context.Context, *types.ListSessionsRequest) (*types.ListSessionsResponse, error)
	GetSession(context.Context, *types.GetSessionRequest) (*types.GetSessionResponse, error)
	ResetSession(context.Context, *types.ResetSessionRequest) (*types.ResetSessionResponse, error)
	DeleteSession(context.Context, *types.DeleteSessionRequest) (*types.DeleteSessionResponse, error)
	ListCommands(context.Context, *types.ListCommandsRequest) (*types.ListCommandsResponse, error)
	ListEvents(context.Context, *aop.ListEventsRequest) (*aop.ListEventsResponse, error)
}

type ScanService interface {
	SubmitScan(context.Context, *types.SubmitScanRequest) (*types.SubmitScanResponse, error)
	GetScan(context.Context, *types.GetScanRequest) (*types.GetScanResponse, error)
	ListScans(context.Context, *types.ListScansRequest) (*types.ListScansResponse, error)
	CancelScan(context.Context, *types.CancelScanRequest) (*types.CancelScanResponse, error)
	GetScanReport(context.Context, *types.GetScanReportRequest) (*types.GetScanReportResponse, error)
}

type ConfigService interface {
	GetConfig(context.Context, *types.GetConfigRequest) (*types.GetConfigResponse, error)
	UpdateConfig(context.Context, *types.UpdateConfigRequest) (*types.UpdateConfigResponse, error)
	ActivateProfile(context.Context, *types.ActivateProfileRequest) (*types.ActivateProfileResponse, error)
	TestLLM(context.Context, *types.LLMProbeRequest) (*types.LLMProbeResult, error)
	ListModels(context.Context, *types.LLMProbeRequest) (*types.ListModelsResult, error)
	TestConnection(context.Context, *types.TestConnectionRequest) (*types.TestConnectionResponse, error)
}

type SCOService interface {
	ListNodes(context.Context, *types.ListNodesRequest) (*types.ListNodesResponse, error)
	GetNode(context.Context, *types.GetNodeRequest) (*types.GetNodeResponse, error)
	GetStats(context.Context, *types.GetStatsRequest) (*types.GetStatsResponse, error)
	DeleteNodes(context.Context, *types.DeleteNodesRequest) (*types.DeleteNodesResponse, error)
	ImportNodes(context.Context, *types.ImportNodesRequest) (*types.ImportNodesResponse, error)
	ListArtifacts(context.Context, *types.ListArtifactsRequest) (*types.ListArtifactsResponse, error)
}

// Management is the protocol-neutral business surface consumed by transport
// adapters. It deliberately hides API's concrete service composition.
type Management interface {
	SessionService() SessionService
	ScanService() ScanService
	ConfigService() ConfigService
	SCOService() SCOService
	ListAgents(*types.ListAgentsRequest) *types.ListAgentsResponse
	GetStatus(*types.GetStatusRequest) *types.GetStatusResponse
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

func (a *API) SessionService() SessionService { return a.Sessions }
func (a *API) ScanService() ScanService       { return a.Scans }
func (a *API) ConfigService() ConfigService   { return a.Config }
func (a *API) SCOService() SCOService         { return a.SCO }

var _ Management = (*API)(nil)

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
