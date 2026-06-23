package agent

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/agent/inbox"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

// Re-export provider types so external consumers only import agent.

type ChatMessage = provider.ChatMessage
type ChatMessageDelta = provider.ChatMessageDelta
type ToolCall = provider.ToolCall
type ToolCallDelta = provider.ToolCallDelta
type FunctionCall = provider.FunctionCall
type FunctionCallDelta = provider.FunctionCallDelta
type ToolDefinition = provider.ToolDefinition
type FunctionDefinition = provider.FunctionDefinition
type ContentPart = provider.ContentPart
type ImageURL = provider.ImageURL
type ChatCompletionRequest = provider.ChatCompletionRequest
type ChatCompletionResponse = provider.ChatCompletionResponse
type ChatCompletionStreamEvent = provider.ChatCompletionStreamEvent
type Choice = provider.Choice
type Usage = provider.Usage
type APIError = provider.APIError
type ResponseFormat = provider.ResponseFormat
type JSONSchemaSpec = provider.JSONSchemaSpec
type CacheRetention = provider.CacheRetention
type Provider = provider.Provider
type StreamingProvider = provider.StreamingProvider
type ProviderConfig = provider.ProviderConfig

const (
	CacheNone  = provider.CacheNone
	CacheShort = provider.CacheShort
	CacheLong  = provider.CacheLong
)

var (
	NewTextMessage       = provider.NewTextMessage
	NewToolResultMessage = provider.NewToolResultMessage
	NewMultimodalMessage = provider.NewMultimodalMessage
	TextPart             = provider.TextPart
	ImagePart            = provider.ImagePart
	ParseDataURI         = provider.ParseDataURI

	NewProvider              = provider.NewProvider
	NewProviderFromResolved  = provider.NewProviderFromResolved
	ResolveProvider          = provider.Resolve
	InferProviderFromBaseURL = provider.InferFromBaseURL
	NormalizeProvider        = provider.NormalizeProvider

	ErrCallTimeout   = provider.ErrCallTimeout
	ErrStreamStalled = provider.ErrStreamStalled
)

// Agent-specific types.

type EventType string

const (
	EventAgentStart         EventType = "agent_start"
	EventAgentEnd           EventType = "agent_end"
	EventTurnStart          EventType = "turn_start"
	EventTurnEnd            EventType = "turn_end"
	EventLLMRequest         EventType = "llm_request"
	EventMessageStart       EventType = "message_start"
	EventMessageUpdate      EventType = "message_update"
	EventMessageEnd         EventType = "message_end"
	EventToolExecutionStart EventType = "tool_execution_start"
	EventToolExecutionEnd   EventType = "tool_execution_end"
	EventTokenBudgetWarning EventType = "token_budget_warning"
	EventEvalStart          EventType = "eval_start"
	EventEvalEnd            EventType = "eval_end"
	EventEvalError          EventType = "eval_error"
)

type StopReason string

const (
	StopReasonCompleted  StopReason = "completed"
	StopReasonTerminated StopReason = "terminated"
	StopReasonStopped    StopReason = "stopped"
	StopReasonBudget     StopReason = "budget"
	StopReasonError      StopReason = "error"
	StopReasonCanceled   StopReason = "canceled"
)

type Event struct {
	Type            EventType
	SessionID       string
	ParentSessionID string
	Turn            int
	EmittedAt       time.Time
	Request         *ChatCompletionRequest
	Message         ChatMessage
	Messages        []ChatMessage
	NewMessages     []ChatMessage
	ToolResults     []ChatMessage
	ToolCallID      string
	ToolName        string
	Arguments       string
	Result          string
	IsError         bool
	Err             error
	StartedAt       time.Time // tool execution start time (set on ToolExecutionEnd)
	Stop            StopReason
	Usage           *Usage
	TotalUsage      *Usage // cumulative usage across all turns (set on TurnEnd/AgentEnd)
	ContextTokens   int
	EvalRound       int
	EvalPass        bool
	EvalReason      string
	EvalError       string
}

type TransformContextFunc func([]ChatMessage) []ChatMessage

type BeforeToolCallContext struct {
	AssistantMessage ChatMessage
	ToolCall         ToolCall
	SystemPrompt     string
	Messages         []ChatMessage
}

type BeforeToolCallResult struct {
	Block  bool
	Reason string
}

type AfterToolCallContext struct {
	AssistantMessage ChatMessage
	ToolCall         ToolCall
	Result           string
	IsError          bool
	SystemPrompt     string
	Messages         []ChatMessage
}

type ToolFlowDecision int

const (
	ToolFlowContinue ToolFlowDecision = iota
	ToolFlowTerminate
)

type AfterToolCallResult struct {
	Result  *string
	IsError *bool
	Flow    ToolFlowDecision
}

// SystemPromptFunc is called at the start of each turn to produce the system prompt.
// Receives the current config context so it can adapt to active tools, model, etc.
type SystemPromptFunc func(cfg *Config) string

type ProviderEntry struct {
	Provider Provider
	Model    string
}

type Config struct {
	Provider         Provider
	Tools            *commands.CommandRegistry
	Model            string
	Fallbacks        []ProviderEntry
	SystemPrompt     string
	SystemPromptFn   SystemPromptFunc
	Messages         []ChatMessage
	MaxTokens        int
	Temperature      *float64
	Stream           bool
	MaxRetries       int
	TokenBudget      int
	ResponseFormat   *ResponseFormat
	Logger           telemetry.Logger
	TransformContext TransformContextFunc
	Bus              *eventbus.Bus[Event]
	BeforeToolCall   func(context.Context, BeforeToolCallContext) (*BeforeToolCallResult, error)
	AfterToolCall    func(context.Context, AfterToolCallContext) (*AfterToolCallResult, error)
	MaxTurns         int
	LoopScheduler    *LoopScheduler
	Inbox            inbox.Inbox
	Expander         *inbox.Expander
	MaxResultSize    int
	MaxParallelTools int
	CacheRetention   CacheRetention
	SessionID        string
	ParentSessionID  string
}

// Builder methods — each returns a modified copy (Config is a value type).

func (c Config) WithProvider(p Provider) Config               { c.Provider = p; return c }
func (c Config) WithTools(t *commands.CommandRegistry) Config { c.Tools = t; return c }
func (c Config) WithModel(m string) Config                    { c.Model = m; return c }
func (c Config) WithSystemPrompt(s string) Config             { c.SystemPrompt = s; return c }
func (c Config) WithMessages(msgs []ChatMessage) Config       { c.Messages = msgs; return c }
func (c Config) WithStream(s bool) Config                     { c.Stream = s; return c }
func (c Config) WithInbox(ib inbox.Inbox) Config              { c.Inbox = ib; return c }
func (c Config) WithLogger(l telemetry.Logger) Config         { c.Logger = l; return c }
func (c Config) WithBus(b *eventbus.Bus[Event]) Config        { c.Bus = b; return c }
func (c Config) WithMaxTokens(n int) Config                   { c.MaxTokens = n; return c }
func (c Config) WithTemperature(t float64) Config             { c.Temperature = &t; return c }
func (c Config) WithMaxRetries(n int) Config                  { c.MaxRetries = n; return c }
func (c Config) WithTokenBudget(n int) Config                 { c.TokenBudget = n; return c }
func (c Config) WithExpander(e *inbox.Expander) Config        { c.Expander = e; return c }
func (c Config) WithTransformContext(fn TransformContextFunc) Config {
	c.TransformContext = fn
	return c
}
func (c Config) WithCacheRetention(r CacheRetention) Config { c.CacheRetention = r; return c }
func (c Config) WithSessionID(id string) Config             { c.SessionID = id; return c }
func (c Config) WithResponseFormat(rf *ResponseFormat) Config {
	c.ResponseFormat = rf
	return c
}
func (c Config) WithLoopScheduler(s *LoopScheduler) Config {
	c.LoopScheduler = s
	return c
}

func (c Config) init() Config {
	if c.Logger == nil {
		c.Logger = telemetry.NopLogger()
	}
	if c.MaxRetries == 0 {
		c.MaxRetries = DefaultMaxRetries
	}
	if c.MaxResultSize <= 0 {
		c.MaxResultSize = DefaultMaxResultSize
	}
	if c.MaxParallelTools <= 0 {
		c.MaxParallelTools = DefaultMaxParallelTools
	}
	if c.SessionID == "" {
		b := make([]byte, 8)
		_, _ = crand.Read(b)
		c.SessionID = hex.EncodeToString(b)
	}
	if c.Tools == nil {
		c.Tools = commands.NewRegistry()
	}
	if c.Inbox == nil {
		c.Inbox = inbox.NewBuffered(SubInboxCapacity)
	}
	if c.Bus == nil {
		c.Bus = eventbus.New[Event]()
	}
	return c
}

type emitter struct {
	bus             *eventbus.Bus[Event]
	sessionID       string
	parentSessionID string
}

func newEmitter(bus *eventbus.Bus[Event], sessionID, parentSessionID string) emitter {
	return emitter{bus: bus, sessionID: sessionID, parentSessionID: parentSessionID}
}

func (e emitter) Emit(ev Event) {
	ev.SessionID = e.sessionID
	ev.ParentSessionID = e.parentSessionID
	ev.EmittedAt = time.Now()
	e.bus.Emit(ev)
}

// NewAgent creates an Agent from a Config.
func NewAgent(cfg Config) *Agent {
	cfg = cfg.init()
	return &Agent{
		Cfg: cfg,
		state: State{
			SystemPrompt: cfg.SystemPrompt,
			Tools:        cfg.Tools,
		},
	}
}

type TurnUsage struct {
	Turn             int `json:"turn"`
	PromptTokens     int `json:"prompt_tokens"`
	CompletionTokens int `json:"completion_tokens"`
	TotalTokens      int `json:"total_tokens"`
	CacheReadTokens  int `json:"cache_read_tokens,omitempty"`
	CacheWriteTokens int `json:"cache_write_tokens,omitempty"`
}

type Result struct {
	Output        string
	NewMessages   []ChatMessage
	Messages      []ChatMessage
	Turns         int
	TotalUsage    Usage
	TurnUsages    []TurnUsage
	ContextTokens int
	Stop          StopReason
	Err           error
}

type State struct {
	SystemPrompt string
	Messages     []ChatMessage
	Tools        *commands.CommandRegistry
	ErrorMessage string
	LastError    error
}
