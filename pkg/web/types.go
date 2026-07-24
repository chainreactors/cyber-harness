package web

import (
	"encoding/json"
	"time"

	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/webproto"
)

type ScanStatus string

const (
	StatusQueued    ScanStatus = "queued"
	StatusRunning   ScanStatus = "running"
	StatusCompleted ScanStatus = "completed"
	StatusFailed    ScanStatus = "failed"
	StatusCanceled  ScanStatus = "canceled"
)

type ScanJob struct {
	ID        string         `json:"id"`
	Target    string         `json:"target"`
	Mode      string         `json:"mode"`
	Verify    bool           `json:"verify,omitempty"`
	Sniper    bool           `json:"sniper,omitempty"`
	Deep      bool           `json:"deep,omitempty"`
	Status    ScanStatus     `json:"status"`
	Progress  string         `json:"progress,omitempty"`
	Report    string         `json:"report,omitempty"`
	Result    *output.Result `json:"result,omitempty"`
	Error     string         `json:"error,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

type ScanRequest struct {
	Target string `json:"target"`
	Mode   string `json:"mode"`
	Verify bool   `json:"verify,omitempty"`
	Sniper bool   `json:"sniper,omitempty"`
	Deep   bool   `json:"deep,omitempty"`
}

type ServiceStatus struct {
	Version             string `json:"version"`
	LLMAvailable        bool   `json:"llm_available"`
	LLMProvider         string `json:"llm_provider,omitempty"`
	LLMModel            string `json:"llm_model,omitempty"`
	LLMAPIKeyConfigured bool   `json:"llm_api_key_configured,omitempty"`
	ConfigPath          string `json:"config_path,omitempty"`
	ConfigLoaded        bool   `json:"config_loaded"`
	Agents              int    `json:"agents"`
	IOAURL              string `json:"ioa_url,omitempty"`
}

// ConfigStatus is the response for GET /api/config — secrets masked,
// *_configured booleans indicate whether a secret is set.
type ConfigStatus struct {
	ConfigPath   string `json:"config_path,omitempty"`
	ConfigLoaded bool   `json:"config_loaded"`
	LLM          struct {
		Provider         string             `json:"provider"`
		BaseURL          string             `json:"base_url"`
		APIKeyConfigured bool               `json:"api_key_configured"`
		Model            string             `json:"model"`
		Proxy            string             `json:"proxy"`
		MaxTokens        int                `json:"max_tokens,omitempty"`
		ContextWindow    int                `json:"context_window,omitempty"`
		ActiveProfile    string             `json:"active_profile,omitempty"`
		Profiles         []LLMProfileStatus `json:"profiles,omitempty"`
	} `json:"llm"`
	Cyberhub struct {
		URL           string `json:"url"`
		KeyConfigured bool   `json:"key_configured"`
		Mode          string `json:"mode"`
		Proxy         string `json:"proxy"`
	} `json:"cyberhub"`
	Recon struct {
		FofaEmail              string `json:"fofa_email"`
		FofaKeyConfigured      bool   `json:"fofa_key_configured"`
		HunterTokenConfigured  bool   `json:"hunter_token_configured"`
		HunterAPIKeyConfigured bool   `json:"hunter_api_key_configured"`
		Proxy                  string `json:"proxy"`
		Limit                  *int   `json:"limit,omitempty"`
	} `json:"recon"`
	Scan struct {
		Verify string `json:"verify"`
	} `json:"scan"`
	Search struct {
		TavilyKeysConfigured bool `json:"tavily_keys_configured"`
	} `json:"search"`
	IOA struct {
		URL             string `json:"url"`
		TokenConfigured bool   `json:"token_configured"`
		NodeName        string `json:"node_name"`
		Space           string `json:"space"`
	} `json:"ioa"`
	Agent struct {
		Tools       []string `json:"tools,omitempty"`
		Timeout     int      `json:"timeout"`
		SaveSession bool     `json:"save_session"`
	} `json:"agent"`
}

type LLMProfileStatus struct {
	ID               string `json:"id"`
	Name             string `json:"name"`
	Provider         string `json:"provider"`
	BaseURL          string `json:"base_url"`
	APIKeyConfigured bool   `json:"api_key_configured"`
	Model            string `json:"model"`
	Proxy            string `json:"proxy"`
	MaxTokens        int    `json:"max_tokens,omitempty"`
	ContextWindow    int    `json:"context_window,omitempty"`
}

// ConfigStatusFromDistribute builds a masked ConfigStatus from raw config.
func ConfigStatusFromDistribute(d *webproto.DistributeConfig, path string, loaded bool) ConfigStatus {
	var cs ConfigStatus
	cs.ConfigPath = path
	cs.ConfigLoaded = loaded
	active := d.LLM.Active()
	cs.LLM.Provider = active.Provider
	cs.LLM.BaseURL = active.BaseURL
	cs.LLM.APIKeyConfigured = active.APIKey != ""
	cs.LLM.Model = active.Model
	cs.LLM.Proxy = active.Proxy
	cs.LLM.MaxTokens = active.MaxTokens
	cs.LLM.ContextWindow = active.ContextWindow
	cs.LLM.ActiveProfile = d.LLM.ActiveProfile
	for _, profile := range d.LLM.Providers {
		cs.LLM.Profiles = append(cs.LLM.Profiles, LLMProfileStatus{
			ID: profile.ID, Name: profile.Name, Provider: profile.Provider,
			BaseURL: profile.BaseURL, APIKeyConfigured: profile.APIKey != "",
			Model: profile.Model, Proxy: profile.Proxy,
			MaxTokens: profile.MaxTokens, ContextWindow: profile.ContextWindow,
		})
	}
	cs.Cyberhub.URL = d.Cyberhub.URL
	cs.Cyberhub.KeyConfigured = d.Cyberhub.Key != ""
	cs.Cyberhub.Mode = d.Cyberhub.Mode
	cs.Cyberhub.Proxy = d.Cyberhub.Proxy
	cs.Recon.FofaEmail = d.Recon.FofaEmail
	cs.Recon.FofaKeyConfigured = d.Recon.FofaKey != ""
	cs.Recon.HunterTokenConfigured = d.Recon.HunterToken != ""
	cs.Recon.HunterAPIKeyConfigured = d.Recon.HunterAPIKey != ""
	cs.Recon.Proxy = d.Recon.Proxy
	cs.Recon.Limit = d.Recon.Limit
	cs.Scan.Verify = d.Scan.Verify
	cs.Search.TavilyKeysConfigured = d.Search.TavilyKeys != ""
	cs.IOA.URL = d.IOA.URL
	cs.IOA.TokenConfigured = d.IOA.Token != ""
	cs.IOA.NodeName = d.IOA.NodeName
	cs.IOA.Space = d.IOA.Space
	cs.Agent.Tools = d.Agent.Tools
	cs.Agent.Timeout = d.Agent.Timeout
	cs.Agent.SaveSession = d.Agent.SaveSession
	return cs
}

// --- Chat types ---

const (
	SessionActive   = "active"
	SessionArchived = "archived"
)

type ChatSession struct {
	ID        string    `json:"id"`
	AgentID   string    `json:"agent_id"`
	AgentName string    `json:"agent_name,omitempty"`
	Title     string    `json:"title"`
	Status    string    `json:"status"`
	TopicID   string    `json:"topic_id,omitempty"`
	ScanIDs   []string  `json:"scan_ids,omitempty"`
	CreatedAt time.Time `json:"created_at"`
	UpdatedAt time.Time `json:"updated_at"`
}

type ChatMessage struct {
	ID        string          `json:"id"`
	SessionID string          `json:"session_id"`
	Role      string          `json:"role"`
	AgentID   string          `json:"agent_id,omitempty"`
	AgentName string          `json:"agent_name,omitempty"`
	Content   string          `json:"content"`
	Metadata  json.RawMessage `json:"metadata,omitempty"`
	CreatedAt time.Time       `json:"created_at"`
	// Queued is a transient send-time hint: true when the message was accepted
	// while another chat task is still running on the session, so the client
	// can render it as pending-in-queue rather than in-flight.
	Queued bool `json:"queued,omitempty"`
}

const (
	ChatEventScanStarted    = "scan_started"
	ChatEventScanProgress   = "scan_progress"
	ChatEventScanComplete   = "scan_complete"
	ChatEventAgentJoined    = "agent_joined"
	ChatEventSessionCleared = "session_cleared"
)

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

type ChatEvent struct {
	Type      string         `json:"type"`
	SessionID string         `json:"session_id"`
	MessageID string         `json:"message_id,omitempty"`
	Role      string         `json:"role,omitempty"`
	AgentID   string         `json:"agent_id,omitempty"`
	AgentName string         `json:"agent_name,omitempty"`
	Turn      int            `json:"turn,omitempty"`
	Content   string         `json:"content,omitempty"`
	ScanID    string         `json:"scan_id,omitempty"`
	Result    *output.Result `json:"result,omitempty"`
	Data      string         `json:"data,omitempty"`
	Transient bool           `json:"-"`
}

type SendMessageRequest struct {
	Content string `json:"content"`
	// Goal-mode run controls (optional). The frontend sends these when the user
	// enables the Goal panel; a plain chat send leaves them zero.
	EvalCriteria    string `json:"eval_criteria,omitempty"`
	EvalMaxRounds   int    `json:"eval_max_rounds,omitempty"`
	PersistMaxTurns int    `json:"persist_max_turns,omitempty"`
}

type CreateSessionRequest struct {
	AgentID string `json:"agent_id"`
	Title   string `json:"title,omitempty"`
}
