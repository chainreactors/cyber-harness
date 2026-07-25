package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	"github.com/chainreactors/aiscan/pkg/aop"
)

// streamEventCollector records message/message.delta events from the bus.
type streamEventCollector struct {
	mu       sync.Mutex
	deltas   []aop.MessageDeltaData
	messages []aop.MessageData
}

func (c *streamEventCollector) handler(event aop.Event) {
	switch event.Type {
	case aop.TypeMessageDelta:
		if d, err := aop.DecodeData[aop.MessageDeltaData](event); err == nil {
			c.mu.Lock()
			c.deltas = append(c.deltas, d)
			c.mu.Unlock()
		}
	case aop.TypeMessage:
		if d, err := aop.DecodeData[aop.MessageData](event); err == nil {
			c.mu.Lock()
			c.messages = append(c.messages, d)
			c.mu.Unlock()
		}
	}
}

func (c *streamEventCollector) assistantMessages() []aop.MessageData {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []aop.MessageData
	for _, m := range c.messages {
		if m.Role == "assistant" {
			out = append(out, m)
		}
	}
	return out
}

func reasoningStreamEvents() []ChatCompletionStreamEvent {
	return []ChatCompletionStreamEvent{
		{Delta: ChatMessageDelta{Role: "assistant"}},
		{Delta: ChatMessageDelta{ReasoningContent: strPtr("think-")}},
		{Delta: ChatMessageDelta{ReasoningContent: strPtr("hard")}},
		{Delta: ChatMessageDelta{Content: strPtr("ans-")}},
		{Delta: ChatMessageDelta{Content: strPtr("wer")}},
		{Done: true},
	}
}

func TestStreamDeltasAndFinalMessageShareMessageID(t *testing.T) {
	collector := &streamEventCollector{}
	llm := &scriptedProvider{streamEvents: reasoningStreamEvents()}

	_, err := (NewAgent(Config{
		Provider: llm,
		Model:    "test",
		Stream:   true,
		Bus:      testBus(collector.handler),
	})).Run(context.Background(), TextInput("hi"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}

	collector.mu.Lock()
	deltas := append([]aop.MessageDeltaData(nil), collector.deltas...)
	collector.mu.Unlock()
	if len(deltas) != 4 {
		t.Fatalf("deltas = %d, want 4", len(deltas))
	}
	messageID := deltas[0].MessageID
	if messageID == "" {
		t.Fatal("delta has empty message_id")
	}
	for _, d := range deltas {
		if d.MessageID != messageID {
			t.Fatalf("delta message_id = %q, want stable %q", d.MessageID, messageID)
		}
		switch d.PartType {
		case aop.PartReasoning:
			if d.PartIndex != 0 {
				t.Fatalf("reasoning delta part_index = %d, want 0", d.PartIndex)
			}
		case aop.PartText:
			if d.PartIndex != 1 {
				t.Fatalf("text delta part_index = %d, want 1 (reasoning present)", d.PartIndex)
			}
		}
	}

	finals := collector.assistantMessages()
	if len(finals) != 1 {
		t.Fatalf("assistant messages = %d, want 1", len(finals))
	}
	if finals[0].MessageID != messageID {
		t.Fatalf("final message id = %q, want delta id %q", finals[0].MessageID, messageID)
	}
	if len(finals[0].Parts) != 2 ||
		finals[0].Parts[0].Type != aop.PartReasoning || finals[0].Parts[0].Text != "think-hard" ||
		finals[0].Parts[1].Type != aop.PartText || finals[0].Parts[1].Text != "ans-wer" {
		t.Fatalf("final parts = %+v", finals[0].Parts)
	}
}

// flakyStreamProvider fails the first stream attempt with a retryable error,
// then streams successfully.
type flakyStreamProvider struct {
	calls  atomic.Int32
	events []ChatCompletionStreamEvent
}

func (p *flakyStreamProvider) Name() string { return "flaky-stream" }

func (p *flakyStreamProvider) ChatCompletion(context.Context, *ChatCompletionRequest) (*ChatCompletionResponse, error) {
	return nil, retryableTimeoutError{}
}

func (p *flakyStreamProvider) ChatCompletionStream(ctx context.Context, _ *ChatCompletionRequest) (<-chan ChatCompletionStreamEvent, error) {
	if p.calls.Add(1) == 1 {
		return nil, retryableTimeoutError{}
	}
	ch := make(chan ChatCompletionStreamEvent)
	go func() {
		defer close(ch)
		for _, event := range p.events {
			select {
			case ch <- event:
			case <-ctx.Done():
				return
			}
		}
	}()
	return ch, nil
}

func TestMessageIDStableAcrossStreamRetry(t *testing.T) {
	collector := &streamEventCollector{}
	llm := &flakyStreamProvider{events: reasoningStreamEvents()}

	_, err := (NewAgent(Config{
		Provider:   llm,
		Model:      "test",
		Stream:     true,
		MaxRetries: 1,
		Bus:        testBus(collector.handler),
	})).Run(context.Background(), TextInput("hi"))
	if err != nil {
		t.Fatalf("Run() error = %v", err)
	}
	if llm.calls.Load() != 2 {
		t.Fatalf("stream calls = %d, want 2", llm.calls.Load())
	}

	collector.mu.Lock()
	deltas := append([]aop.MessageDeltaData(nil), collector.deltas...)
	collector.mu.Unlock()
	if len(deltas) == 0 {
		t.Fatal("no deltas recorded")
	}
	messageID := deltas[0].MessageID
	for _, d := range deltas {
		if d.MessageID != messageID {
			t.Fatalf("delta id %q differs from %q after retry", d.MessageID, messageID)
		}
	}
	finals := collector.assistantMessages()
	if len(finals) != 1 {
		t.Fatalf("assistant messages = %d, want exactly 1 across retries", len(finals))
	}
	if finals[0].MessageID != messageID {
		t.Fatalf("final message id = %q, want %q", finals[0].MessageID, messageID)
	}
}
