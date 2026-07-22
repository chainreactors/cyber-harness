package agent

import (
	"context"
	crand "crypto/rand"
	"encoding/hex"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/tool"
	"github.com/chainreactors/aiscan/pkg/agent/inbox"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/aop/x/delegation"
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

type StopReason string

const (
	StopReasonCompleted  StopReason = "completed"
	StopReasonTerminated StopReason = "terminated"
	StopReasonStopped    StopReason = "stopped"
	StopReasonBudget     StopReason = "budget"
	StopReasonError      StopReason = "error"
	StopReasonCanceled   StopReason = "canceled"
)

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
	Tools            tool.Executor
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
	Logger           telemetry.Logger
	TransformContext TransformContextFunc
	Bus              *eventbus.Bus[aop.Event]
	// OnRunEnd fires once per run with the final result — replaces the old
	// EventAgentEnd Messages subscription for session persistence.
	OnRunEnd         func(*Result)
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
	ParentToolCallID string
	Delegation       *delegation.DelegationDetail
	// AgentName tags emitted AOP events; defaults to "aiscan".
	AgentName string
	// MessageCounter seeds message_id allocation ("m-<n>") when a session is
	// restored; Result.MessageCounter carries the final value for saving.
	MessageCounter int64

	emitter *aopEmitter
}

// Builder methods — each returns a modified copy (Config is a value type).

func (c Config) WithProvider(p Provider) Config            { c.Provider = p; return c }
func (c Config) WithTools(t tool.Executor) Config          { c.Tools = t; return c }
func (c Config) WithModel(m string) Config                 { c.Model = m; return c }
func (c Config) WithSystemPrompt(s string) Config          { c.SystemPrompt = s; return c }
func (c Config) WithMessages(msgs []ChatMessage) Config    { c.Messages = msgs; return c }
func (c Config) WithStream(s bool) Config                  { c.Stream = s; return c }
func (c Config) WithInbox(ib inbox.Inbox) Config           { c.Inbox = ib; return c }
func (c Config) WithLogger(l telemetry.Logger) Config      { c.Logger = l; return c }
func (c Config) WithBus(b *eventbus.Bus[aop.Event]) Config { c.Bus = b; return c }
func (c Config) WithMaxTokens(n int) Config                { c.MaxTokens = n; return c }
func (c Config) WithTemperature(t float64) Config          { c.Temperature = &t; return c }
func (c Config) WithMaxRetries(n int) Config               { c.MaxRetries = n; return c }
func (c Config) WithTokenBudget(n int) Config              { c.TokenBudget = n; return c }
func (c Config) WithExpander(e *inbox.Expander) Config     { c.Expander = e; return c }
func (c Config) WithTransformContext(fn TransformContextFunc) Config {
	c.TransformContext = fn
	return c
}
func (c Config) WithCacheRetention(r CacheRetention) Config { c.CacheRetention = r; return c }
func (c Config) WithSessionID(id string) Config             { c.SessionID = id; return c }
func (c Config) WithAgentName(name string) Config           { c.AgentName = name; return c }
func (c Config) WithOnRunEnd(fn func(*Result)) Config       { c.OnRunEnd = fn; return c }
func (c Config) WithLoopScheduler(s *LoopScheduler) Config {
	c.LoopScheduler = s
	return c
}

func (c Config) init() Config {
	if c.Logger == nil {
		c.Logger = telemetry.NopLogger()
	}
	if c.MaxRetries <= 0 {
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
	if c.AgentName == "" {
		c.AgentName = "aiscan"
	}
	if c.Tools == nil {
		c.Tools = tool.EmptyExecutor()
	}
	if c.Inbox == nil {
		c.Inbox = inbox.NewBuffered(SubInboxCapacity)
	}
	if c.Bus == nil {
		c.Bus = eventbus.New[aop.Event]()
	}
	if c.emitter == nil {
		c.emitter = newAOPEmitter(c.Bus, c.AgentName, c.SessionID, c.ParentSessionID, c.ParentToolCallID, c.Delegation, c.MessageCounter)
	}
	return c
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
	Output         string
	NewMessages    []ChatMessage
	Messages       []ChatMessage
	Turns          int
	TotalUsage     Usage
	TurnUsages     []TurnUsage
	ContextTokens  int
	Stop           StopReason
	Err            error
	MessageCounter int64
}

type State struct {
	SystemPrompt string
	Messages     []ChatMessage
	Tools        tool.Executor
	ErrorMessage string
	LastError    error
}
