package agent

import (
	"context"
	"fmt"
	"testing"
)

// TestSetProviderHotSwapsNextRun verifies a mid-conversation provider swap takes
// effect on the next run (an in-flight run keeps its snapshotted provider).
func TestSetProviderHotSwapsNextRun(t *testing.T) {
	provA := &callbackProvider{fn: func(_ context.Context, _ *ChatCompletionRequest) (*ChatCompletionResponse, error) {
		return chatResponse(NewTextMessage("assistant", "from-A")), nil
	}}
	provB := &callbackProvider{fn: func(_ context.Context, _ *ChatCompletionRequest) (*ChatCompletionResponse, error) {
		return chatResponse(NewTextMessage("assistant", "from-B")), nil
	}}

	ag := NewAgent(Config{Provider: provA, Model: "model-a"})

	res, err := ag.Run(context.Background(), TextInput("hi"))
	if err != nil {
		t.Fatalf("run A: %v", err)
	}
	if res.Output != "from-A" {
		t.Fatalf("run A output = %q, want from-A", res.Output)
	}

	ag.SetProvider(provB, "model-b")

	res, err = ag.Run(context.Background(), TextInput("hi again"))
	if err != nil {
		t.Fatalf("run B: %v", err)
	}
	if res.Output != "from-B" {
		t.Fatalf("run B output = %q, want from-B", res.Output)
	}
	if ag.Cfg.Model != "model-b" {
		t.Fatalf("model = %q, want model-b", ag.Cfg.Model)
	}

	// Empty model must not blank the current one (provider-only swap).
	ag.SetProvider(provA, "")
	if ag.Cfg.Model != "model-b" {
		t.Fatalf("empty-model swap changed model to %q, want model-b", ag.Cfg.Model)
	}
}

// TestSetProviderRaceWithRun exercises a config push swapping the provider while
// runs execute; run under -race it proves the Cfg read/write are serialized.
func TestSetProviderRaceWithRun(t *testing.T) {
	prov := &callbackProvider{fn: func(_ context.Context, _ *ChatCompletionRequest) (*ChatCompletionResponse, error) {
		return chatResponse(NewTextMessage("assistant", "ok")), nil
	}}
	ag := NewAgent(Config{Provider: prov, Model: "m"})

	done := make(chan struct{})
	go func() {
		defer close(done)
		for i := 0; i < 50; i++ {
			ag.SetProvider(prov, fmt.Sprintf("m-%d", i))
		}
	}()
	for i := 0; i < 50; i++ {
		if _, err := ag.Run(context.Background(), TextInput("hi")); err != nil {
			t.Errorf("run %d: %v", i, err)
		}
	}
	<-done
}
