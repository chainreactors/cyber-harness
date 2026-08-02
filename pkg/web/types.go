package web

import (
	"errors"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
	config "github.com/chainreactors/aiscan/core/config"
	configpb "github.com/chainreactors/aiscan/pkg/types/config"
	scanpb "github.com/chainreactors/aiscan/pkg/types/scan"
	"google.golang.org/protobuf/types/known/timestamppb"
)

var (
	ErrScanNotFound      = errors.New("scan not found")
	ErrScanNotCancelable = errors.New("scan cannot be canceled")
	ErrSessionNotFound   = errors.New("session not found")
	ErrTurnNotFound      = errors.New("turn not found")
)

// Session states stored in aop.Session.State. The SQLite status column uses
// the same values; migrate() rewrites the legacy active/archived rows.
const (
	SessionStateOpen   = "open"
	SessionStateClosed = "closed"
)

// scanStatusToDB maps the proto enum to the string stored in the scans.status
// column. UNSPECIFIED and unknown values round-trip as "queued".
func scanStatusToDB(value scanpb.ScanStatus) string {
	switch value {
	case scanpb.ScanStatus_SCAN_STATUS_RUNNING:
		return "running"
	case scanpb.ScanStatus_SCAN_STATUS_COMPLETED:
		return "completed"
	case scanpb.ScanStatus_SCAN_STATUS_FAILED:
		return "failed"
	case scanpb.ScanStatus_SCAN_STATUS_CANCELED:
		return "canceled"
	default:
		return "queued"
	}
}

func scanTerminal(value scanpb.ScanStatus) bool {
	return value == scanpb.ScanStatus_SCAN_STATUS_COMPLETED ||
		value == scanpb.ScanStatus_SCAN_STATUS_FAILED ||
		value == scanpb.ScanStatus_SCAN_STATUS_CANCELED
}

func nowProto() *timestamppb.Timestamp { return timestamppb.New(time.Now()) }

// ConfigViewFromDistribute builds the secret-masked product view directly in
// the schema owned by aiscan.config.
func ConfigViewFromDistribute(d *configpb.DistributeConfig, path string, loaded bool) *configpb.ConfigView {
	view := &configpb.ConfigView{Path: path, Loaded: loaded}
	if d == nil {
		return view
	}
	view.Llm = &configpb.LLMView{ActiveProfile: d.GetLlm().GetActiveProfile()}
	for _, raw := range d.GetLlm().GetProviders() {
		profile := config.NormalizeLLMProvider(raw)
		if profile == nil {
			continue
		}
		item := &configpb.LLMProviderView{
			Id: profile.Id, Name: profile.Name, Provider: profile.Provider,
			BaseUrl: profile.BaseUrl, ApiKeyConfigured: profile.ApiKey != "",
			Model: profile.Model, Proxy: profile.Proxy,
			MaxTokens: profile.MaxTokens, ContextWindow: profile.ContextWindow,
		}
		view.Llm.Providers = append(view.Llm.Providers, item)
		if profile.Id == view.Llm.ActiveProfile {
			view.Llm.Active = item
		}
	}
	if view.Llm.Active == nil && len(view.Llm.Providers) > 0 {
		view.Llm.Active = view.Llm.Providers[0]
		view.Llm.ActiveProfile = view.Llm.Active.Id
	}
	view.Cyberhub = &configpb.CyberhubView{
		Url: d.GetCyberhub().GetUrl(), KeyConfigured: d.GetCyberhub().GetKey() != "",
		Mode: d.GetCyberhub().GetMode(), Proxy: d.GetCyberhub().GetProxy(),
	}
	view.Recon = &configpb.ReconView{
		FofaEmail: d.GetRecon().GetFofaEmail(), FofaKeyConfigured: d.GetRecon().GetFofaKey() != "",
		HunterTokenConfigured:  d.GetRecon().GetHunterToken() != "",
		HunterApiKeyConfigured: d.GetRecon().GetHunterApiKey() != "",
		Proxy:                  d.GetRecon().GetProxy(), Limit: d.GetRecon().GetLimit(),
	}
	view.Scan = &configpb.ScanConfig{Verify: d.GetScan().GetVerify()}
	view.Search = &configpb.SearchView{TavilyKeysConfigured: d.GetSearch().GetTavilyKeys() != ""}
	view.Ioa = &configpb.IOAView{
		Url: d.GetIoa().GetUrl(), TokenConfigured: d.GetIoa().GetToken() != "",
		NodeName: d.GetIoa().GetNodeName(), Space: d.GetIoa().GetSpace(),
	}
	view.Agent = &configpb.AgentConfig{
		Tools:   append([]string(nil), d.GetAgent().GetTools()...),
		Timeout: d.GetAgent().GetTimeout(), SaveSession: d.GetAgent().GetSaveSession(),
	}
	return view
}

// --- Chat types ---

type persistedAOPEvent struct {
	Cursor int64
	Event  *aop.Event
}

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
