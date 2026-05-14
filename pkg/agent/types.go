package agent

import (
	"context"

	"github.com/chainreactors/aiscan/pkg/provider"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/command"
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
	EventToolExecutionStart  EventType = "tool_execution_start"
	EventToolExecutionEnd    EventType = "tool_execution_end"
	EventTokenBudgetWarning  EventType = "token_budget_warning"
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
	Model               string
	SystemPrompt        string
	MaxTokens           int
	Temperature         *float64
	Stream              bool
	MaxRetries          int
	TokenBudget         int
	Logger              telemetry.Logger
	TransformContext    TransformContextFunc
	Emit                EventHandler
	BeforeToolCall      func(context.Context, BeforeToolCallContext) (*BeforeToolCallResult, error)
	AfterToolCall       func(context.Context, AfterToolCallContext) (*AfterToolCallResult, error)
	ShouldStopAfterTurn func(context.Context, ShouldStopAfterTurnContext) (bool, error)
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

func WithMaxRetries(n int) Option {
	return func(c *Config) { c.MaxRetries = n }
}

func WithTokenBudget(budget int) Option {
	return func(c *Config) { c.TokenBudget = budget }
}
