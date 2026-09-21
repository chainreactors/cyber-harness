package session

import (
	"context"
	"fmt"
	"sync"

	"github.com/chainreactors/cyber/agent"
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
