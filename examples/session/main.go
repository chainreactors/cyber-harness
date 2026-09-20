// Command session demonstrates an embedded conversation without a network model.
package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"

	"github.com/chainreactors/cyber/agent/provider"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/pkg/harness"
)

func main() {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt)
	defer stop()
	if err := run(ctx); err != nil {
		fmt.Fprintln(os.Stderr, err)
		os.Exit(1)
	}
}

// demoProvider reports the history it receives. It performs no inference or I/O.
type demoProvider struct{}

func (demoProvider) Name() string { return "demo" }
func (demoProvider) ChatCompletion(ctx context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	users := 0
	for _, message := range req.Messages {
		if message.Role == "user" {
			users++
		}
	}
	return &provider.ChatCompletionResponse{Choices: []provider.Choice{{
		Message:      provider.TextMessage("assistant", fmt.Sprintf("Demo provider received %d user message(s).", users)),
		FinishReason: "stop",
	}}}, nil
}

func run(ctx context.Context) (resultErr error) {
	directory, err := os.Getwd()
	if err != nil {
		return err
	}
	h, err := harness.New(harness.Config{
		Base: harness.BaseConfig{
			Directory: directory, SkillExclude: []string{"cyber"},
			Provider: provider.StartupConfig{Mode: provider.StartupDisabled},
		},
		Session: &agentsession.Config{
			PrimarySessionID: "main",
		},
	})
	if err != nil {
		return err
	}
	// Cleanup gets its own context even when the host request was canceled.
	defer func() { resultErr = errors.Join(resultErr, h.Close(context.Background())) }()
	if err := h.Load(ctx); err != nil {
		return err
	}
	runtime, err := h.Runtime()
	if err != nil {
		return err
	}
	runtime.SetProvider(demoProvider{}, provider.ProviderConfig{Model: "demo"})
	sub := runtime.Observe(events.ObserverFunc(func(event *aop.Event) {
		if ended := event.GetTurnEnded(); ended != nil {
			fmt.Printf("Turn ended: %s\n", ended.StopReason)
		}
	}))
	defer func() { resultErr = errors.Join(resultErr, sub.Close(context.Background())) }()
	session, err := runtime.OpenSession(ctx, agentsession.SessionOptions{ID: "main"})
	if err != nil {
		return err
	}
	for _, input := range []string{"Remember this conversation.", "Continue using the same history."} {
		turn, err := session.Run(ctx, agentsession.RunInput{Content: []*aop.Content{aop.Text(input)}})
		if err != nil {
			return err
		}
		result, err := turn.Wait()
		if err != nil {
			return err
		}
		fmt.Println(result.Output)
	}
	return runtime.CloseSession(context.Background(), session.ID(), agentsession.SessionCloseCompleted)
}
