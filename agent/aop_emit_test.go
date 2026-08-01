package agent

import (
	"context"
	"sync"
	"sync/atomic"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
	ext "github.com/chainreactors/aiscan/aop/aiscan/extensions"
	"github.com/chainreactors/aiscan/core/eventbus"
)

// streamEventCollector records message/message.delta events from the bus.
type streamEventCollector struct {
	mu       sync.Mutex
	deltas   []*aop.MessageDelta
	messages []*aop.Message
}

func (c *streamEventCollector) handler(event *aop.Event) {
	switch eventKind(event) {
	case "message.delta":
		if d := event.GetMessageDelta(); d != nil {
			c.mu.Lock()
			c.deltas = append(c.deltas, d)
			c.mu.Unlock()
		}
	case "message":
		if d := event.GetMessage(); d != nil {
			c.mu.Lock()
			c.messages = append(c.messages, d)
			c.mu.Unlock()
		}
	}
}

func (c *streamEventCollector) assistantMessages() []*aop.Message {
	c.mu.Lock()
	defer c.mu.Unlock()
	var out []*aop.Message
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
	deltas := append([]*aop.MessageDelta(nil), collector.deltas...)
	collector.mu.Unlock()
	if len(deltas) != 4 {
		t.Fatalf("deltas = %d, want 4", len(deltas))
	}
	messageID := deltas[0].MessageId
	if messageID == "" {
		t.Fatal("delta has empty message_id")
	}
	for _, d := range deltas {
		if d.MessageId != messageID {
			t.Fatalf("delta message_id = %q, want stable %q", d.MessageId, messageID)
		}
		switch d.Value.(type) {
		case *aop.MessageDelta_Reasoning:
			if d.ContentIndex != 0 {
				t.Fatalf("reasoning delta content_index = %d, want 0", d.ContentIndex)
			}
		case *aop.MessageDelta_Text:
			if d.ContentIndex != 1 {
				t.Fatalf("text delta content_index = %d, want 1 (reasoning present)", d.ContentIndex)
			}
		}
	}

	finals := collector.assistantMessages()
	if len(finals) != 1 {
		t.Fatalf("assistant messages = %d, want 1", len(finals))
	}
	if finals[0].Id != messageID {
		t.Fatalf("final message id = %q, want delta id %q", finals[0].Id, messageID)
	}
	if len(finals[0].Content) != 2 ||
		finals[0].Content[0].GetReasoning().GetText() != "think-hard" ||
		finals[0].Content[1].GetText().GetText() != "ans-wer" {
		t.Fatalf("final content = %+v", finals[0].Content)
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
	deltas := append([]*aop.MessageDelta(nil), collector.deltas...)
	collector.mu.Unlock()
	if len(deltas) == 0 {
		t.Fatal("no deltas recorded")
	}
	messageID := deltas[0].MessageId
	for _, d := range deltas {
		if d.MessageId != messageID {
			t.Fatalf("delta id %q differs from %q after retry", d.MessageId, messageID)
		}
	}
	finals := collector.assistantMessages()
	if len(finals) != 1 {
		t.Fatalf("assistant messages = %d, want exactly 1 across retries", len(finals))
	}
	if finals[0].Id != messageID {
		t.Fatalf("final message id = %q, want %q", finals[0].Id, messageID)
	}
}

func TestStatusPreservesTypedExtensionNamespace(t *testing.T) {
	bus := eventbus.New[*aop.Event]()
	var emitted *aop.Event
	bus.Subscribe(func(event *aop.Event) { emitted = event })
	emitter := newAOPEmitter(bus, "agent-1", "session-1", "", "", nil, 0)
	emitter.status(ext.CompactStateEnd, ext.CompactNamespace, &ext.CompactDetail{
		TokensBefore: 1000,
		TokensAfter:  400,
		KeptMessages: 8,
	})

	if emitted == nil || emitted.GetStatus().GetState() != ext.CompactStateEnd {
		t.Fatalf("status event = %+v", emitted)
	}
	detail, ok, err := ext.GetCompactDetail(emitted)
	if err != nil || !ok || detail.TokensBefore != 1000 || detail.TokensAfter != 400 || detail.KeptMessages != 8 {
		t.Fatalf("compact detail = %+v, ok=%v, err=%v", detail, ok, err)
	}
	if emitted.GetStatus().Detail != nil {
		t.Fatal("status detail must have one canonical representation in event.extensions")
	}
}

func TestToolResultEmitterPreservesAllProtocolFields(t *testing.T) {
	bus := eventbus.New[*aop.Event]()
	var emitted *aop.Event
	bus.Subscribe(func(event *aop.Event) { emitted = event })
	emitter := newAOPEmitter(bus, "agent-1", "session-1", "", "", nil, 0).turn("turn-1")
	emitter.toolResult("call-1", "scan", []*aop.Content{
		aop.Text("done"),
		aop.Image("image/png", []byte("image")),
	}, map[string]any{"ports": 3}, true, true, 12)

	result := emitted.GetToolResult()
	if result == nil || result.CallId != "call-1" || result.Name != "scan" || !result.Terminate || !result.IsError || result.DurationMs != 12 {
		t.Fatalf("tool result = %+v", result)
	}
	if len(result.Output) != 2 || result.Output[0].GetText().GetText() != "done" || string(result.Output[1].GetMedia().GetResource().GetData()) != "image" {
		t.Fatalf("tool result output = %+v", result.Output)
	}
	detail, err := aop.DecodeJSON[map[string]int](result.Detail)
	if err != nil || detail["ports"] != 3 {
		t.Fatalf("tool result detail = %+v, err=%v", detail, err)
	}
}
