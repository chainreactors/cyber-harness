package agent

import (
	"context"
	"fmt"

	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/agent/inbox"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)


type EventType string

const (
	EventAgentStart         EventType = "agent_start"
	EventAgentEnd           EventType = "agent_end"
	EventTurnStart          EventType = "turn_start"
	EventTurnEnd            EventType = "turn_end"
	EventMessageStart       EventType = "message_start"
	EventMessageUpdate      EventType = "message_update"
	EventMessageEnd         EventType = "message_end"
	EventToolExecutionStart EventType = "tool_execution_start"
	EventToolExecutionEnd   EventType = "tool_execution_end"
	EventTokenBudgetWarning EventType = "token_budget_warning"
)

type StopReason string

const (
	StopReasonCompleted  StopReason = "completed"
	StopReasonTerminated StopReason = "terminated"
	StopReasonStopped    StopReason = "stopped"
	StopReasonBudget     StopReason = "budget"
	StopReasonError      StopReason = "error"
	StopReasonCancelled  StopReason = "cancelled"
)

type Event struct {
	Type        EventType
	Turn        int
	Message     provider.ChatMessage
	Messages    []provider.ChatMessage
	NewMessages []provider.ChatMessage
	ToolResults []provider.ChatMessage
	ToolCallID  string
	ToolName    string
	Arguments   string
	Result      string
	IsError     bool
	Err         error
	Stop        StopReason
}

type EventHandler func(context.Context, Event) error

type TransformContextFunc func([]provider.ChatMessage) []provider.ChatMessage

type BeforeToolCallContext struct {
	AssistantMessage provider.ChatMessage
	ToolCall         provider.ToolCall
	SystemPrompt     string
	Messages         []provider.ChatMessage
}

type BeforeToolCallResult struct {
	Block  bool
	Reason string
}

type AfterToolCallContext struct {
	AssistantMessage provider.ChatMessage
	ToolCall         provider.ToolCall
	Result           string
	IsError          bool
	SystemPrompt     string
	Messages         []provider.ChatMessage
}

type ToolFlowDecision int

const (
	ToolFlowContinue  ToolFlowDecision = iota
	ToolFlowTerminate
)

type AfterToolCallResult struct {
	Result  *string
	IsError *bool
	Flow    ToolFlowDecision
}

type ShouldStopAfterTurnContext struct {
	Message      provider.ChatMessage
	ToolResults  []provider.ChatMessage
	SystemPrompt string
	Messages     []provider.ChatMessage
	Tools        *command.CommandRegistry
	NewMessages  []provider.ChatMessage
}

type Config struct {
	Provider            provider.Provider
	Tools               *command.CommandRegistry
	Model               string
	SystemPrompt        string
	Messages            []provider.ChatMessage
	MaxTokens           int
	Temperature         *float64
	Stream              bool
	MaxRetries          int
	TokenBudget         int
	ResponseFormat      *provider.ResponseFormat
	Logger              telemetry.Logger
	TransformContext    TransformContextFunc
	Emit                EventHandler
	BeforeToolCall      func(context.Context, BeforeToolCallContext) (*BeforeToolCallResult, error)
	AfterToolCall       func(context.Context, AfterToolCallContext) (*AfterToolCallResult, error)
	ShouldStopAfterTurn func(context.Context, ShouldStopAfterTurnContext) (bool, error)
	LoopScheduler *LoopScheduler
	Inbox         inbox.Inbox
	Expander      *inbox.Expander
	MaxResultSize int
}

// Builder methods — each returns a modified copy (Config is a value type).

func (c Config) WithProvider(p provider.Provider) Config             { c.Provider = p; return c }
func (c Config) WithTools(t *command.CommandRegistry) Config         { c.Tools = t; return c }
func (c Config) WithModel(m string) Config                           { c.Model = m; return c }
func (c Config) WithSystemPrompt(s string) Config                    { c.SystemPrompt = s; return c }
func (c Config) WithMessages(msgs []provider.ChatMessage) Config     { c.Messages = msgs; return c }
func (c Config) WithStream(s bool) Config                            { c.Stream = s; return c }
func (c Config) WithInbox(ib inbox.Inbox) Config                     { c.Inbox = ib; return c }
func (c Config) WithLogger(l telemetry.Logger) Config                { c.Logger = l; return c }
func (c Config) WithEventHandler(h EventHandler) Config              { c.Emit = h; return c }
func (c Config) WithMaxTokens(n int) Config                          { c.MaxTokens = n; return c }
func (c Config) WithTemperature(t float64) Config                    { c.Temperature = &t; return c }
func (c Config) WithMaxRetries(n int) Config                         { c.MaxRetries = n; return c }
func (c Config) WithTokenBudget(n int) Config                        { c.TokenBudget = n; return c }
func (c Config) WithExpander(e *inbox.Expander) Config               { c.Expander = e; return c }
func (c Config) WithTransformContext(fn TransformContextFunc) Config { c.TransformContext = fn; return c }
func (c Config) WithResponseFormat(rf *provider.ResponseFormat) Config {
	c.ResponseFormat = rf
	return c
}
func (c Config) WithLoopScheduler(s *LoopScheduler) Config {
	c.LoopScheduler = s
	return c
}

// DeriveChild creates a child config inheriting provider, tools, model,
// and logger from the parent. Per-session fields (Inbox, LoopScheduler, Emit,
// SystemPrompt, Messages, hooks) are not inherited.
func (c Config) DeriveChild() Config {
	return Config{
		Provider:    c.Provider,
		Tools:       c.Tools,
		Model:       c.Model,
		Logger:      c.Logger,
		MaxRetries:  c.MaxRetries,
		Stream:      c.Stream,
		Temperature: c.Temperature,
	}
}

// RunWithContext executes a one-shot agent task inheriting parent messages.
// Used by fork mode: child sees parent's full conversation + new directive,
// maximizing prompt cache hit on the shared prefix.
func (c Config) RunWithContext(ctx context.Context, prompt string, parentMessages []provider.ChatMessage) (*Result, error) {
	cfg := normalizeConfig(c)
	if cfg.Tools == nil {
		cfg.Tools = command.NewRegistry()
	}
	if cfg.Inbox == nil {
		cfg.Inbox = inbox.NewBuffered(8)
	}
	if err := cfg.Inbox.Push(inbox.NewUserMessage(prompt)); err != nil {
		return nil, fmt.Errorf("push initial prompt: %w", err)
	}
	cfg.Messages = parentMessages
	return runLoop(ctx, cfg)
}

// Run executes a one-shot agent task and returns the result.
func (c Config) Run(ctx context.Context, prompt string) (*Result, error) {
	cfg := normalizeConfig(c)
	if cfg.Tools == nil {
		cfg.Tools = command.NewRegistry()
	}
	if cfg.Inbox == nil {
		cfg.Inbox = inbox.NewBuffered(8)
	}
	if err := cfg.Inbox.Push(inbox.NewUserMessage(prompt)); err != nil {
		return nil, fmt.Errorf("push initial prompt: %w", err)
	}
	return runLoop(ctx, cfg)
}

// NewAgent creates a reusable Agent instance for multi-turn interaction.
func (c Config) NewAgent() *Agent {
	cfg := normalizeConfig(c)
	if cfg.Tools == nil {
		cfg.Tools = command.NewRegistry()
	}
	return &Agent{
		provider: cfg.Provider,
		tools:    cfg.Tools,
		config:   cfg,
		emit:     cfg.Emit,
		state: State{
			SystemPrompt:     cfg.SystemPrompt,
			Tools:            cfg.Tools,
			PendingToolCalls: make(map[string]struct{}),
		},
		done: closedChan(),
	}
}

// NewAgent creates a reusable Agent from a Config.
func NewAgent(cfg Config) *Agent { return cfg.NewAgent() }

type Result struct {
	Output      string
	NewMessages []provider.ChatMessage
	Messages    []provider.ChatMessage
	Turns       int
	TotalUsage  provider.Usage
	Err         error
}

type State struct {
	SystemPrompt     string
	Messages         []provider.ChatMessage
	Tools            *command.CommandRegistry
	IsRunning        bool
	StreamingMessage *provider.ChatMessage
	PendingToolCalls map[string]struct{}
	ErrorMessage     string
	LastError        error
}
