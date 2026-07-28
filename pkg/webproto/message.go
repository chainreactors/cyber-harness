package webproto

import (
	"encoding/json"
	"fmt"

	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/core/aop"
	"github.com/chainreactors/ioa/protocols"
	"github.com/chainreactors/utils/pty"
)

type Message struct {
	Type    string          `json:"type"`
	TurnID  string          `json:"turn_id,omitempty"`
	TaskID  string          `json:"task_id,omitempty"`
	Data    string          `json:"data,omitempty"`
	DataB64 string          `json:"data_b64,omitempty"`
	Payload json.RawMessage `json:"payload,omitempty"`
}

const (
	TypeSessionOpen   = "session.open"
	TypeSessionOpened = "session.opened"
	TypeSessionClose  = "session.close"
	TypeSessionClosed = "session.closed"
	TypeRun           = "run"
	TypeRunCancel     = "run.cancel"
	TypeCommand       = "command"
	TypeCommandResult = "command.result"
	TypeAOP           = "aop"
	TypeError         = "error"
)

type SessionOpenPayload struct {
	SessionID        string `json:"session_id"`
	ParentSessionID  string `json:"parent_session_id,omitempty"`
	ParentToolCallID string `json:"parent_tool_call_id,omitempty"`
}

type SessionLifecyclePayload struct {
	SessionID string `json:"session_id"`
	Reason    string `json:"reason,omitempty"`
}

type RunPayload struct {
	SessionID     string            `json:"session_id"`
	Parts         []aop.MessagePart `json:"parts"`
	Continue      bool              `json:"continue,omitempty"`
	NoEcho        bool              `json:"no_echo,omitempty"`
	MaxTurns      int               `json:"max_turns,omitempty"`
	EvalCriteria  string            `json:"eval_criteria,omitempty"`
	EvalMaxRounds int               `json:"eval_max_rounds,omitempty"`
}

type CommandPayload struct {
	SessionID string `json:"session_id"`
	Line      string `json:"line"`
}

type ErrorPayload struct {
	Message string `json:"message"`
}

// CommandSpec is the surface-neutral description of one user-facing "/verb" command.
type CommandSpec struct {
	Name        string   `json:"name"`
	Aliases     []string `json:"aliases,omitempty"`
	Usage       string   `json:"usage,omitempty"`
	Description string   `json:"description,omitempty"`
}

type RegisterPayload struct {
	Name string `json:"name"`
	// Commands is the LLM tool/pseudo-command registry (pkg/commands) the agent
	// exposes to the model — distinct from CommandsMenu.
	Commands []string `json:"commands,omitempty"`
	// Tools is the structured LLM tool catalog exposed by this node. Commands
	// remains the shell/pseudo-command catalog used by the slash menu and Bash.
	Tools []tool.Definition `json:"tools,omitempty"`
	// CommandsMenu is the agent's user-facing "/verb" catalog: the agent-scope,
	// menu-visible commands it can run, plus one per loaded skill. The hub merges
	// these with its own hub-scope commands to drive the web "/" menu and /help,
	// so the surfaces never drift.
	CommandsMenu []CommandSpec     `json:"commands_menu,omitempty"`
	Node         protocols.NodeRef `json:"node"`
	Runtime      AgentRuntime      `json:"runtime,omitempty"`
	Status       AgentStatus       `json:"status,omitempty"`
	Stats        AgentStats        `json:"stats,omitempty"`
}

// AgentRuntime describes the process exposing an IOA node over the Web
// transport. It is operational metadata, not another identity.
type AgentRuntime struct {
	Hostname     string         `json:"hostname,omitempty"`
	Username     string         `json:"username,omitempty"`
	WorkingDir   string         `json:"working_dir,omitempty"`
	OS           string         `json:"os,omitempty"`
	Arch         string         `json:"arch,omitempty"`
	PID          int            `json:"pid,omitempty"`
	Capabilities []string       `json:"capabilities,omitempty"`
	Meta         map[string]any `json:"meta,omitempty"`
}

// AgentStatus contains mutable state. Identity remains the immutable NodeRef.
type AgentStatus struct {
	Provider    string `json:"provider,omitempty"`
	Model       string `json:"model,omitempty"`
	Space       string `json:"space,omitempty"`
	Bound       bool   `json:"bound"`
	ConfigError string `json:"config_error,omitempty"`
}

type ConfigReloadResult struct {
	OK       bool   `json:"ok"`
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Error    string `json:"error,omitempty"`
}

type AgentStats struct {
	Turns            int    `json:"turns,omitempty"`
	ToolCalls        int    `json:"tool_calls,omitempty"`
	RunningTools     int    `json:"running_tools,omitempty"`
	PromptTokens     int    `json:"prompt_tokens,omitempty"`
	CompletionTokens int    `json:"completion_tokens,omitempty"`
	TotalTokens      int    `json:"total_tokens,omitempty"`
	CacheReadTokens  int    `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int    `json:"cache_write_tokens,omitempty"`
	Assets           int    `json:"assets,omitempty"`
	Loots            int    `json:"loots,omitempty"`
	LastEvent        string `json:"last_event,omitempty"`
}

// GoalExt carries HTTP chat options that are copied into a RunPayload by the
// web boundary. It is not an AOP RPC envelope or a separate runtime lifecycle.
type GoalExt struct {
	EvalCriteria    string `json:"eval_criteria,omitempty"`
	EvalMaxRounds   int    `json:"eval_max_rounds,omitempty"`
	PersistMaxTurns int    `json:"persist_max_turns,omitempty"`
	// NoEcho suppresses the agent-side echo of this user message; the hub
	// already persisted and broadcast its own copy.
	NoEcho bool `json:"no_echo,omitempty"`
}

// NSWeb is the AOP extension namespace the hub uses to attach its own message
// metadata (originating agent id, persisted metadata) to message events.
const NSWeb = "aiscan.web"

// WebMessageExt is the hub-owned message extension stored under NSWeb.
type WebMessageExt struct {
	AgentID  string          `json:"agent_id,omitempty"`
	Metadata json.RawMessage `json:"metadata,omitempty"`
	// Params carries i18n interpolation values for hub-emitted error events
	// (paired with ErrorData.Code).
	Params map[string]any `json:"params,omitempty"`
}

// SetWebExt writes the hub message extension onto an event.
func SetWebExt(event *aop.Event, ext WebMessageExt) error {
	return aop.SetExt(event, NSWeb, ext)
}

// GetWebExt reads the hub message extension from an event.
func GetWebExt(event aop.Event) (WebMessageExt, bool, error) {
	return aop.Ext[WebMessageExt](event, NSWeb)
}

type FileUploadPayload struct {
	Filename  string `json:"filename"`
	FileSize  int64  `json:"file_size"`
	MimeType  string `json:"mime_type,omitempty"`
	SessionID string `json:"session_id,omitempty"`
}

type FileUploadResult struct {
	Filename string `json:"filename"`
	Path     string `json:"path"`
	Size     int64  `json:"size"`
	Error    string `json:"error,omitempty"`
}

// FileRPCPayload carries the target path for WebAgent file operations. The
// file bytes travel in Message.DataB64 so this transport remains JSON-only.
type FileRPCPayload struct {
	Path string `json:"path"`
	Size int64  `json:"size,omitempty"`
}

// FileEntry is one structured directory entry returned by a file.list RPC.
// Names are transported as JSON strings, so unusual characters never need to
// be inferred from shell output.
type FileEntry struct {
	Name        string `json:"name"`
	IsDirectory bool   `json:"isDirectory"`
	Size        int64  `json:"size"`
}

// FileListResult is carried in the completion payload for file.list.
type FileListResult struct {
	Path    string      `json:"path"`
	Entries []FileEntry `json:"entries"`
}

const TypePTY = "pty"

func NewPTYMessage(frame pty.Frame) Message {
	payload, _ := json.Marshal(frame)
	return Message{Type: TypePTY, Payload: payload}
}

func DecodePTYMessage(msg Message) (pty.Frame, error) {
	if msg.Type != TypePTY {
		return pty.Frame{}, fmt.Errorf("unsupported PTY envelope %q", msg.Type)
	}
	var frame pty.Frame
	if len(msg.Payload) == 0 {
		return frame, fmt.Errorf("PTY frame payload is required")
	}
	if err := json.Unmarshal(msg.Payload, &frame); err != nil {
		return frame, fmt.Errorf("decode PTY frame: %w", err)
	}
	if frame.Type == "" {
		return frame, fmt.Errorf("PTY frame type is required")
	}
	return frame, nil
}

func MustJSON(v any) json.RawMessage {
	data, _ := json.Marshal(v)
	return data
}
