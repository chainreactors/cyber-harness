package agent

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"

	aop "github.com/chainreactors/aiscan/aop"
)

func TestProviderFrameCapturePreservesExactBytesAndIsOptIn(t *testing.T) {
	responseBody := []byte(`{"id":"chatcmpl-1","choices":[{"message":{"role":"assistant","content":"ok"},"finish_reason":"stop"}],"usage":{"prompt_tokens":1,"completion_tokens":1,"total_tokens":2},"x_unknown":{"nested":[1,true]}}`)
	requests := make(chan []byte, 2)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(responseBody)
	}))
	defer server.Close()

	newProvider := func() Provider {
		provider, err := NewProvider(&ProviderConfig{
			Provider: "openai", BaseURL: server.URL + "/v1", APIKey: "secret", Model: "test", Timeout: 5,
		})
		if err != nil {
			t.Fatal(err)
		}
		return provider
	}

	var frames []*aop.ProviderFrame
	_, err := NewAgent(Config{
		Provider: newProvider(), Model: "test", CaptureProviderFrames: true,
		Bus: testBus(func(event *aop.Event) {
			if frame := event.GetProviderFrame(); frame != nil {
				frames = append(frames, frame)
			}
		}),
	}).Run(context.Background(), TextInput("hello"))
	if err != nil {
		t.Fatal(err)
	}
	requestBody := <-requests
	if len(frames) != 2 {
		t.Fatalf("provider frames = %d, want request and response", len(frames))
	}
	if frames[0].Direction != aop.Direction_DIRECTION_REQUEST || string(frames[0].Payload) != string(requestBody) {
		t.Fatalf("request frame = %+v, body=%s", frames[0], requestBody)
	}
	if frames[1].Direction != aop.Direction_DIRECTION_RESPONSE || string(frames[1].Payload) != string(responseBody) {
		t.Fatalf("response frame = %+v", frames[1])
	}
	if len(frames[0].Metadata) != 0 || len(frames[1].Metadata) != 0 {
		t.Fatalf("provider credentials or headers leaked into metadata: %+v", frames)
	}

	frames = nil
	_, err = NewAgent(Config{
		Provider: newProvider(), Model: "test", CaptureProviderFrames: false,
		Bus: testBus(func(event *aop.Event) {
			if frame := event.GetProviderFrame(); frame != nil {
				frames = append(frames, frame)
			}
		}),
	}).Run(context.Background(), TextInput("hello"))
	if err != nil {
		t.Fatal(err)
	}
	<-requests
	if len(frames) != 0 {
		t.Fatalf("provider frames emitted while capture disabled: %+v", frames)
	}
}

func TestAnthropicProviderFrameCapturePreservesExactBytes(t *testing.T) {
	responseBody := []byte(`{"id":"msg_1","type":"message","role":"assistant","content":[{"type":"text","text":"ok"}],"stop_reason":"end_turn","usage":{"input_tokens":1,"output_tokens":1},"x_unknown":{"raw":"kept"}}`)
	requests := make(chan []byte, 1)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)
		requests <- body
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(responseBody)
	}))
	defer server.Close()
	provider, err := NewProvider(&ProviderConfig{
		Provider: "anthropic", BaseURL: server.URL + "/v1", APIKey: "secret", Model: "claude-test", Timeout: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	var frames []*aop.ProviderFrame
	_, err = NewAgent(Config{
		Provider: provider, Model: "claude-test", CaptureProviderFrames: true,
		Bus: testBus(func(event *aop.Event) {
			if frame := event.GetProviderFrame(); frame != nil {
				frames = append(frames, frame)
			}
		}),
	}).Run(context.Background(), TextInput("hello"))
	if err != nil {
		t.Fatal(err)
	}
	requestBody := <-requests
	if len(frames) != 2 || frames[0].Protocol != "anthropic" || string(frames[0].Payload) != string(requestBody) || string(frames[1].Payload) != string(responseBody) {
		t.Fatalf("anthropic provider frames = %+v", frames)
	}
}

func TestProviderFrameCapturePreservesSSEFrameOrder(t *testing.T) {
	chunks := [][]byte{
		[]byte(`{"choices":[{"delta":{"role":"assistant"},"finish_reason":""}],"unknown":1}`),
		[]byte(`{"choices":[{"delta":{"content":"ok"},"finish_reason":"stop"}],"unknown":{"nested":true}}`),
		[]byte(`[DONE]`),
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		for _, chunk := range chunks {
			_, _ = w.Write(append(append([]byte("data: "), chunk...), '\n', '\n'))
		}
	}))
	defer server.Close()
	provider, err := NewProvider(&ProviderConfig{
		Provider: "openai", BaseURL: server.URL + "/v1", APIKey: "secret", Model: "test", Timeout: 5,
	})
	if err != nil {
		t.Fatal(err)
	}
	var frames []*aop.ProviderFrame
	_, err = NewAgent(Config{
		Provider: provider, Model: "test", Stream: true, CaptureProviderFrames: true,
		Bus: testBus(func(event *aop.Event) {
			if frame := event.GetProviderFrame(); frame != nil {
				frames = append(frames, frame)
			}
		}),
	}).Run(context.Background(), TextInput("hello"))
	if err != nil {
		t.Fatal(err)
	}
	if len(frames) != 4 {
		t.Fatalf("provider frames = %d, want request plus 3 SSE frames", len(frames))
	}
	for index, chunk := range chunks {
		frame := frames[index+1]
		if frame.Transport != "sse" || string(frame.Payload) != string(chunk) {
			t.Fatalf("SSE frame %d = %+v, want %s", index, frame, chunk)
		}
	}
}
