package sessionexec

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/subagent"
	aop "github.com/chainreactors/cyber/aop"
	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/internal/testutil/apptest"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
	loopext "github.com/chainreactors/cyber/pkg/exts/agent"
	promptext "github.com/chainreactors/cyber/pkg/exts/prompt"
	sessionext "github.com/chainreactors/cyber/pkg/exts/session"
)

type testTool struct {
	*Tool
	registry     *subagent.Registry
	closeRuntime func(context.Context) error
}

func newSubagentTestTool(t *testing.T, cfg agent.Config) *testTool {
	t.Helper()
	base := cfg.Lifetime
	if base == nil {
		base = t.Context()
	}
	stream, _ := cfg.Bus.(*coreevents.Stream)
	application := apptest.NewFixture(t, cfg.Logger, stream)
	application.Providers.Set(cfg.Provider, agent.ProviderConfig{Model: cfg.Model})
	values := apptest.Entries(t, application)
	registry := cfg.Hooks
	if registry == nil {
		registry = hooks.New()
	}
	values[0] = extension.Provided[*hooks.Registry](registry)
	loop := cfg.Loop
	if loop == nil {
		loop = agent.NoLoop()
	}
	se := sessionext.New(session.Config{})
	values = append(values, promptext.New(), loopext.New(loop), se)
	hosttest.Load(t, t.Context(), values...)
	rt := se.Runtime()
	if _, err := rt.OpenSession(base, session.SessionOptions{ID: cfg.SessionID, SingleTask: true}); err != nil {
		t.Fatal(err)
	}
	definitions := subagent.NewRegistry()
	if err := definitions.Activate(t.Context()); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := definitions.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	tool := New(rt, definitions, base)
	t.Cleanup(func() {
		if err := tool.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	return &testTool{Tool: tool, registry: definitions, closeRuntime: se.Close}
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
