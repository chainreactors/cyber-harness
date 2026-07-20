package webproto

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/chainreactors/aiscan/pkg/ptywire"
	"github.com/chainreactors/ioa/protocols"
	"github.com/chainreactors/utils/pty"
)

type Message struct {
	Type     string          `json:"type"`
	TaskID   string          `json:"task_id,omitempty"`
	StreamID string          `json:"stream_id,omitempty"`
	Data     string          `json:"data,omitempty"`
	DataB64  string          `json:"data_b64,omitempty"`
	Payload  json.RawMessage `json:"payload,omitempty"`
}

// ExecPayload carries structured parameters for an "exec" message.
// When Payload is populated, Command/Cwd/Timeout/Env are used instead of
// the legacy Data field. If Payload is empty, Data is treated as the command.
type ExecPayload struct {
	Command string            `json:"command"`
	Cwd     string            `json:"cwd,omitempty"`
	Timeout int               `json:"timeout,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
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
	Provider string `json:"provider,omitempty"`
	Model    string `json:"model,omitempty"`
	Space    string `json:"space,omitempty"`
	Bound    bool   `json:"bound"`
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

// ChatPayload is the WS payload for a "chat" message: it scopes the remote
// agent conversation to a web session and carries optional Goal-mode run
// controls. Empty EvalCriteria means a plain turn; a non-empty one makes the
// agent run the evaluator loop against the criteria for up to EvalMaxRounds.
type ChatPayload struct {
	SessionID       string        `json:"session_id,omitempty"`
	EvalCriteria    string        `json:"eval_criteria,omitempty"`
	EvalMaxRounds   int           `json:"eval_max_rounds,omitempty"`
	PersistMaxTurns int           `json:"persist_max_turns,omitempty"`
	SystemPrompt    string        `json:"system_prompt,omitempty"`
	OutputSchema    *OutputSchema `json:"output_schema,omitempty"`
}

type OutputSchema struct {
	Name   string          `json:"name"`
	Schema json.RawMessage `json:"schema"`
}

func DecodeChatPayload(raw json.RawMessage) (ChatPayload, error) {
	var payload ChatPayload
	if len(raw) == 0 {
		return payload, nil
	}
	if err := json.Unmarshal(raw, &payload); err != nil {
		return ChatPayload{}, fmt.Errorf("decode chat payload: %w", err)
	}
	payload.SessionID = strings.TrimSpace(payload.SessionID)
	payload.EvalCriteria = strings.TrimSpace(payload.EvalCriteria)
	payload.SystemPrompt = strings.TrimSpace(payload.SystemPrompt)
	return payload, nil
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

type PTYPayload = ptywire.Payload

func MessageToFrame(msg Message) (pty.Frame, error) {
	return ptywire.ToFrame(ptywire.Message{
		Type: msg.Type, StreamID: msg.StreamID, Data: msg.Data,
		DataB64: msg.DataB64, Payload: msg.Payload,
	})
}

func FrameToMessage(frame pty.Frame) Message {
	msg := ptywire.FromFrame(frame)
	return Message{Type: msg.Type, StreamID: msg.StreamID, Data: msg.Data, DataB64: msg.DataB64, Payload: msg.Payload}
}

func DecodePTYPayload(raw json.RawMessage) (PTYPayload, error) {
	return ptywire.DecodePayload(raw)
}

func MustJSON(v any) json.RawMessage {
	return ptywire.MustJSON(v)
}
