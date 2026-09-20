package session

import (
	"context"
	"fmt"
	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/inbox"
	aop "github.com/chainreactors/cyber/aop"
	coreevents "github.com/chainreactors/cyber/core/events"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
	"sync"
	"testing"
)

func newSubagentTestTool(t *testing.T, cfg agent.Config) *SubAgentTool {
	t.Helper()
	base := cfg.Lifetime
	if base == nil {
		base = t.Context()
	}
	ctx, cancel := context.WithCancel(base)
	events, _ := cfg.Bus.(*coreevents.Stream)
	if events == nil {
		events = coreevents.New()
	}
	rt := &Runtime{ctx: ctx, cancel: cancel, loaded: true, agentConfig: cfg, events: events, sessions: make(map[string]*sessionState), runs: make(map[string]*Run)}
	if cfg.Inbox == nil {
		cfg.Inbox = inbox.NewBuffered(64)
	}
	parent, err := rt.OpenSession(ctx, SessionOptions{ID: cfg.SessionID})
	if err != nil {
		t.Fatal(err)
	}
	parent.state.inbox.base = cfg.Inbox
	parent.state.inbox.active = true
	rt.hooks = cfg.Hooks
	t.Cleanup(func() { _ = rt.close(context.Background()) })
	return NewSubAgentTool(rt, nil)
}

type scriptedProvider struct {
	mu        sync.Mutex
	responses []*agent.ChatCompletionResponse
	requests  []*agent.ChatCompletionRequest
}

func (p *scriptedProvider) Name() string { return "scripted" }
func (p *scriptedProvider) ChatCompletion(_ context.Context, req *agent.ChatCompletionRequest) (*agent.ChatCompletionResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	p.requests = append(p.requests, req)
	if len(p.responses) == 0 {
		return nil, fmt.Errorf("no scripted response left")
	}
	response := p.responses[0]
	p.responses = p.responses[1:]
	return response, nil
}
func (p *scriptedProvider) requestsSnapshot() []*agent.ChatCompletionRequest {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]*agent.ChatCompletionRequest(nil), p.requests...)
}
func chatResponse(msg *aop.Message) *agent.ChatCompletionResponse {
	return &agent.ChatCompletionResponse{Choices: []agent.Choice{{Message: msg}}}
}
func newTestTools(t testing.TB, tools ...coretool.Tool) coretool.Executor {
	return hosttest.Tools(t, tools...)
}

type callbackProvider struct {
	fn func(context.Context, *agent.ChatCompletionRequest) (*agent.ChatCompletionResponse, error)
}

func (p *callbackProvider) Name() string { return "callback" }
func (p *callbackProvider) ChatCompletion(ctx context.Context, req *agent.ChatCompletionRequest) (*agent.ChatCompletionResponse, error) {
	return p.fn(ctx, req)
}

func newTextMessage(role, text string) *aop.Message {
	return &aop.Message{Role: role, Content: []*aop.Content{aop.Text(text)}}
}
