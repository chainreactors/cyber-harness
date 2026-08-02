package web

import (
	"errors"
	"time"

	types "github.com/chainreactors/aiscan/pkg/types"
	managementapi "github.com/chainreactors/aiscan/pkg/web/api"
	"google.golang.org/protobuf/types/known/timestamppb"
)

// ConfigStore and PreparedConfig are the host integration surface for config
// persistence; the business semantics live in web/api.
type ConfigStore = managementapi.ConfigStore
type PreparedConfig = managementapi.PreparedConfig

var (
	ErrScanNotFound      = managementapi.ErrScanNotFound
	ErrScanNotCancelable = managementapi.ErrScanNotCancelable
	ErrSessionNotFound   = errors.New("session not found")
	ErrTurnNotFound      = managementapi.ErrTurnNotFound
)

// Session states stored in aop.Session.State. The SQLite status column uses
// the same values; migrate() rewrites the legacy active/archived rows.
const (
	SessionStateOpen   = managementapi.SessionStateOpen
	SessionStateClosed = managementapi.SessionStateClosed
)

// scanStatusToDB maps the proto enum to the string stored in the scans.status
// column. UNSPECIFIED and unknown values round-trip as "queued".
func scanStatusToDB(value types.ScanStatus) string {
	switch value {
	case types.ScanStatus_SCAN_STATUS_RUNNING:
		return "running"
	case types.ScanStatus_SCAN_STATUS_COMPLETED:
		return "completed"
	case types.ScanStatus_SCAN_STATUS_FAILED:
		return "failed"
	case types.ScanStatus_SCAN_STATUS_CANCELED:
		return "canceled"
	default:
		return "queued"
	}
}

func nowProto() *timestamppb.Timestamp { return timestamppb.New(time.Now()) }

// System message codes. A backend-generated system message carries a stable
// Code (+ optional Params) so the client can localize it via i18n; Content
// holds an English fallback for non-i18n consumers, logs and tests. Keys are
// mirrored under `sys.*` in web/frontend/src/i18n/locales/*/chat.ts.
const (
	SysNoRunningTask     = "no_running_task"
	SysPaused            = "paused"
	SysFileUploaded      = "file_uploaded" // params: filename, path
	SysNoAgentsConnected = "no_agents_connected"
	SysAgentsList        = "agents_list" // params: count, agents[]
	SysAgentNotConnected = "agent_not_connected"
)
