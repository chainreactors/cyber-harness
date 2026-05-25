package agent

import (
	"context"

	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/pkg/agent/inbox"
	"github.com/chainreactors/aiscan/pkg/agent/provider"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

const maxResultSize = 50 * 1024

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
}

type EventHandler func(context.Context, Event) error

type TransformContextFunc func([]provider.ChatMessage) []provider.ChatMessage

type BeforeToolCallContext struct {
	AssistantMessage provider.ChatMessage
	ToolCall         provider.ToolCall
	Context          Context
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
	Context          Context
}

type AfterToolCallResult struct {
	Result    *string
	IsError   *bool
	Terminate bool
}

type ShouldStopAfterTurnContext struct {
	Message     provider.ChatMessage
	ToolResults []provider.ChatMessage
	Context     Context
	NewMessages []provider.ChatMessage
}

type Config struct {
	Provider            provider.Provider
	Tools               *command.CommandRegistry
	Model               string
	SystemPrompt        string
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
	KeepAlive           func() bool
	Inbox               inbox.Inbox
	Expander            *inbox.Expander
}

// Builder methods — each returns a modified copy (Config is a value type).

func (c Config) WithProvider(p provider.Provider) Config { c.Provider = p; return c }
func (c Config) WithTools(t *command.CommandRegistry) Config { c.Tools = t; return c }
func (c Config) WithModel(m string) Config { c.Model = m; return c }
func (c Config) WithSystemPrompt(s string) Config { c.SystemPrompt = s; return c }
func (c Config) WithStream(s bool) Config { c.Stream = s; return c }
func (c Config) WithInbox(ib inbox.Inbox) Config { c.Inbox = ib; return c }
func (c Config) WithLogger(l telemetry.Logger) Config { c.Logger = l; return c }
func (c Config) WithEventHandler(h EventHandler) Config { c.Emit = h; return c }
func (c Config) WithMaxTokens(n int) Config { c.MaxTokens = n; return c }
func (c Config) WithTokenBudget(n int) Config { c.TokenBudget = n; return c }
func (c Config) WithExpander(e *inbox.Expander) Config { c.Expander = e; return c }
func (c Config) WithKeepAlive(fn func() bool) Config {
	c.KeepAlive = fn
	return c
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
	cfg.Inbox.Push(inbox.NewUserMessage(prompt))
	return runLoop(ctx, Context{
		SystemPrompt: cfg.SystemPrompt,
		Messages:     parentMessages,
		Tools:        cfg.Tools,
	}, cfg)
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
	cfg.Inbox.Push(inbox.NewUserMessage(prompt))
	return runLoop(ctx, Context{
		SystemPrompt: cfg.SystemPrompt,
		Tools:        cfg.Tools,
	}, cfg)
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

type Option func(*Config)

type Context struct {
	SystemPrompt string
	Messages     []provider.ChatMessage
	Tools        *command.CommandRegistry
}

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

func WithTools(tools *command.CommandRegistry) Option {
	return func(c *Config) { c.Tools = tools }
}

func WithProvider(p provider.Provider) Option {
	return func(c *Config) { c.Provider = p }
}

func WithModel(model string) Option {
	return func(c *Config) { c.Model = model }
}

func WithSystemPrompt(prompt string) Option {
	return func(c *Config) { c.SystemPrompt = prompt }
}

func WithMaxTokens(maxTokens int) Option {
	return func(c *Config) { c.MaxTokens = maxTokens }
}

func WithTemperature(temperature float64) Option {
	return func(c *Config) { c.Temperature = &temperature }
}

func WithStream(stream bool) Option {
	return func(c *Config) { c.Stream = stream }
}

func WithLogger(logger telemetry.Logger) Option {
	return func(c *Config) { c.Logger = logger }
}

func WithTransformContext(fn TransformContextFunc) Option {
	return func(c *Config) { c.TransformContext = fn }
}

func WithEventHandler(emit EventHandler) Option {
	return func(c *Config) { c.Emit = emit }
}

func WithBeforeToolCall(fn func(context.Context, BeforeToolCallContext) (*BeforeToolCallResult, error)) Option {
	return func(c *Config) { c.BeforeToolCall = fn }
}

func WithAfterToolCall(fn func(context.Context, AfterToolCallContext) (*AfterToolCallResult, error)) Option {
	return func(c *Config) { c.AfterToolCall = fn }
}

func WithShouldStopAfterTurn(fn func(context.Context, ShouldStopAfterTurnContext) (bool, error)) Option {
	return func(c *Config) { c.ShouldStopAfterTurn = fn }
}

func WithKeepAlive(fn func() bool) Option {
	return func(c *Config) { c.KeepAlive = fn }
}

func WithMaxRetries(n int) Option {
	return func(c *Config) { c.MaxRetries = n }
}

func WithTokenBudget(budget int) Option {
	return func(c *Config) { c.TokenBudget = budget }
}

func WithResponseFormat(rf *provider.ResponseFormat) Option {
	return func(c *Config) { c.ResponseFormat = rf }
}

func WithInbox(ib inbox.Inbox) Option {
	return func(c *Config) { c.Inbox = ib }
}

func WithExpander(e *inbox.Expander) Option {
	return func(c *Config) { c.Expander = e }
}
