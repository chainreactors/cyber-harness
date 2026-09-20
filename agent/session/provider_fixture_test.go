package session

import (
	"context"
	"fmt"
	"sync"
	"testing"

	"github.com/chainreactors/cyber/agent"
	aop "github.com/chainreactors/cyber/aop"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
)

type taskLoop func(context.Context, agent.Config) (*agent.Result, error)

func (f taskLoop) Run(ctx context.Context, cfg agent.Config) (*agent.Result, error) {
	return f(ctx, cfg)
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
