package runner

import (
	"bufio"
	"bytes"
	"context"
	"errors"
	"github.com/chainreactors/aiscan/agent"
	"github.com/chainreactors/aiscan/agent/provider"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/telemetry"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/encoding/protojson"
	protobuf "google.golang.org/protobuf/proto"
	"io"
	"strings"
	"sync"
	"testing"
	"time"
)

func newTestStdioHost(output io.Writer) *stdioHost {
	return newStdioHost(context.Background(), nil, telemetry.NopLogger(), output)
}

func protocolLine(t *testing.T, id string, message protobuf.Message) string {
	t.Helper()
	data, err := protojson.Marshal(aop.MustWrap(id, "", message))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func openSessionLine(t *testing.T, sessionID string) string {
	id := "open-" + sessionID
	return protocolLine(t, id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_OpenSessionRequest{OpenSessionRequest: &aop.OpenSessionRequest{
		SessionId: sessionID,
	}}})
}

func runLine(t *testing.T, sessionID, turnID, text string) string {
	return protocolLine(t, turnID, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_RunTurnRequest{RunTurnRequest: &aop.RunTurnRequest{
		SessionId: sessionID, TurnId: turnID,
		Input: &aop.Message{Id: "input-" + turnID, Role: "user", Content: []*aop.Content{aop.Text(text)}},
	}}})
}

func closeSessionLine(t *testing.T, sessionID, reason string) string {
	id := "close-" + sessionID
	return protocolLine(t, id, &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CloseSessionRequest{CloseSessionRequest: &aop.CloseSessionRequest{
		SessionId: sessionID, Reason: reason,
	}}})
}

func TestStdioAcceptRejectsMalformedJSON(t *testing.T) {
	var output bytes.Buffer
	h := newTestStdioHost(&output)
	h.accept("not json")
	envelopes := decodeEnvelopes(t, &output)
	message := unwrapCore(t, envelopes[0])
	if len(envelopes) != 1 || message.GetProtocolError() == nil || !strings.Contains(message.GetProtocolError().Message, "decode frame") {
		t.Fatalf("envelopes = %#v", envelopes)
	}
}

func TestStdioAcceptRejectsUnsupportedFrame(t *testing.T) {
	var output bytes.Buffer
	h := newTestStdioHost(&output)
	h.accept(protocolLine(t, "future", &aop.ProtocolMessage{}))
	envelopes := decodeEnvelopes(t, &output)
	if len(envelopes) != 1 || unwrapCore(t, envelopes[0]).GetProtocolError() == nil || envelopes[0].ReplyTo != "future" {
		t.Fatalf("envelopes = %#v", envelopes)
	}
}

func TestStdioRunRequiresOpenSession(t *testing.T) {
	var output bytes.Buffer
	h := newRuntimeStdioHost(t, &output, nil)
	defer h.rt.Close()
	h.accept(runLine(t, "s1", "turn-1", "hello"))
	envelopes := decodeEnvelopes(t, &output)
	if len(envelopes) != 1 || unwrapCore(t, envelopes[0]).GetRunTurnResponse().GetRejected() == nil {
		t.Fatalf("envelopes = %#v", envelopes)
	}
}

func TestStdioRunRejectsEmptyPrompt(t *testing.T) {
	var output bytes.Buffer
	h := newRuntimeStdioHost(t, &output, nil)
	defer h.rt.Close()
	h.accept(openSessionLine(t, "s1"))
	h.accept(runLine(t, "s1", "turn-1", "   "))
	h.drain()
	envelopes := decodeEnvelopes(t, &output)
	var rejected bool
	for _, envelope := range envelopes {
		message, err := aop.Unwrap(envelope)
		if err == nil {
			if core, ok := message.(*aop.ProtocolMessage); ok && core.GetRunTurnResponse().GetRejected() != nil {
				rejected = true
			}
		}
	}
	if !rejected {
		t.Fatalf("envelopes = %#v", envelopes)
	}
}

func TestStdioCommandUsesIndependentCorrelationID(t *testing.T) {
	var output bytes.Buffer
	h := newRuntimeStdioHost(t, &output, nil)
	defer h.rt.Close()
	h.accept(openSessionLine(t, "s1"))
	h.accept(protocolLine(t, "command-correlation", &types.CommandProtocolMessage{Message: &types.CommandProtocolMessage_Request{Request: &types.CommandRequest{
		SessionId: "s1", Line: "/help",
	}}}))
	h.drain()
	for _, envelope := range decodeEnvelopes(t, &output) {
		message, err := aop.Unwrap(envelope)
		command, ok := message.(*types.CommandProtocolMessage)
		if err != nil || !ok || command.GetResult() == nil {
			continue
		}
		if envelope.ReplyTo != "command-correlation" {
			t.Fatalf("command result correlation = %+v", envelope)
		}
		return
	}
	t.Fatal("command result missing")
}

func TestStdioHostReportsEncoderFailure(t *testing.T) {
	h := newTestStdioHost(failingWriter{})
	h.emitError("", errors.New("broken"))
	if err := h.err(); err == nil || !strings.Contains(err.Error(), "write stdio protocol") {
		t.Fatalf("host err = %v", err)
	}
}

func TestStdioDrainWithoutRuns(t *testing.T) {
	var output bytes.Buffer
	newTestStdioHost(&output).drain()
}

func decodeEnvelopes(t *testing.T, input *bytes.Buffer) []*aop.Envelope {
	t.Helper()
	var envelopes []*aop.Envelope
	scanner := bufio.NewScanner(bytes.NewReader(input.Bytes()))
	for scanner.Scan() {
		envelope := new(aop.Envelope)
		if err := protojson.Unmarshal(scanner.Bytes(), envelope); err != nil {
			t.Fatal(err)
		}
		envelopes = append(envelopes, envelope)
	}
	if err := scanner.Err(); err != nil {
		t.Fatal(err)
	}
	return envelopes
}

func unwrapCore(t *testing.T, envelope *aop.Envelope) *aop.ProtocolMessage {
	t.Helper()
	message, err := aop.Unwrap(envelope)
	if err != nil {
		t.Fatal(err)
	}
	core, ok := message.(*aop.ProtocolMessage)
	if !ok {
		t.Fatalf("message = %T", message)
	}
	return core
}

func decodeAOPMessages(envelopes []*aop.Envelope) []*aop.Event {
	var events []*aop.Event
	for _, envelope := range envelopes {
		message, err := aop.Unwrap(envelope)
		if err != nil {
			continue
		}
		if core, ok := message.(*aop.ProtocolMessage); ok && core.GetEvent() != nil {
			events = append(events, core.GetEvent())
		}
	}
	return events
}

type failingWriter struct{}

func (failingWriter) Write([]byte) (int, error) { return 0, errors.New("broken pipe") }

// stdioGateProvider blocks every call until the gate closes, recording the
// user prompt of each call in start order.
type stdioGateProvider struct {
	gate chan struct{}

	mu      sync.Mutex
	prompts []string
}

func newStdioGateProvider() *stdioGateProvider {
	return &stdioGateProvider{gate: make(chan struct{})}
}

func (p *stdioGateProvider) Name() string { return "stdio-gate" }

func (p *stdioGateProvider) ChatCompletion(ctx context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	p.mu.Lock()
	p.prompts = append(p.prompts, lastUserText(req.Messages))
	p.mu.Unlock()
	select {
	case <-p.gate:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return &provider.ChatCompletionResponse{
		Choices: []provider.Choice{{Message: provider.TextMessage("assistant", "done")}},
	}, nil
}

func (p *stdioGateProvider) callCount() int {
	p.mu.Lock()
	defer p.mu.Unlock()
	return len(p.prompts)
}

func (p *stdioGateProvider) promptsSnapshot() []string {
	p.mu.Lock()
	defer p.mu.Unlock()
	return append([]string(nil), p.prompts...)
}

func lastUserText(messages []*aop.Message) string {
	for i := len(messages) - 1; i >= 0; i-- {
		if messages[i].Role == "user" {
			return provider.MessageText(messages[i])
		}
	}
	return ""
}

func newStdioTestSession(t *testing.T, h *stdioHost, output *bytes.Buffer, id string, prov agent.Provider) {
	t.Helper()
	if h.rt == nil || h.rt.ctx == nil {
		initRuntimeStdioHost(t, h, prov)
	}
	h.accept(openSessionLine(t, id))
}

func newRuntimeStdioHost(t *testing.T, output *bytes.Buffer, prov agent.Provider) *stdioHost {
	t.Helper()
	h := newStdioHost(context.Background(), nil, nil, output)
	initRuntimeStdioHost(t, h, prov)
	return h
}

func initRuntimeStdioHost(t *testing.T, h *stdioHost, prov agent.Provider) {
	t.Helper()
	h.rt = newBareRuntime(t, nil, prov)
	h.rt.config.Model = "test"
	h.rt.config.MaxTurns = 4
	h.rt.Subscribe(func(event *aop.Event) {
		_ = h.emit(aop.MustWrap(runtimeEnvelopeID(), "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_Event{Event: event}}))
	})
}

func waitForCalls(t *testing.T, prov *stdioGateProvider, n int, what string) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for time.Now().Before(deadline) {
		if prov.callCount() >= n {
			return
		}
		time.Sleep(5 * time.Millisecond)
	}
	t.Fatalf("timed out waiting for %s (calls = %d, want %d)", what, prov.callCount(), n)
}

func TestStdioSameSessionFIFOOrder(t *testing.T) {
	var output bytes.Buffer
	h := newTestStdioHost(&output)
	prov := newStdioGateProvider()
	newStdioTestSession(t, h, &output, "s1", prov)
	defer h.rt.Close()

	for _, text := range []string{"first", "second", "third"} {
		h.accept(runLine(t, "s1", "turn-"+text, text))
	}
	waitForCalls(t, prov, 1, "first run to start")
	close(prov.gate)
	h.drain()

	prompts := prov.promptsSnapshot()
	if len(prompts) != 3 || prompts[0] != "first" || prompts[1] != "second" || prompts[2] != "third" {
		t.Fatalf("prompt order = %v, want [first second third]", prompts)
	}
}

func TestStdioSessionsRunConcurrently(t *testing.T) {
	var output bytes.Buffer
	prov := newStdioGateProvider()
	h := newRuntimeStdioHost(t, &output, prov)
	defer h.rt.Close()

	h.accept(openSessionLine(t, "s1"))
	h.accept(openSessionLine(t, "s2"))
	h.accept(runLine(t, "s1", "turn-one", "one"))
	h.accept(runLine(t, "s2", "turn-two", "two"))

	// Both sessions are mid-run at the same time: neither FIFO blocks the other.
	waitForCalls(t, prov, 2, "both session runs to start")

	close(prov.gate)
	h.drain()
	h.accept(closeSessionLine(t, "s1", "completed"))
	h.accept(closeSessionLine(t, "s2", "completed"))

	// Interleaved output must stay valid AOP: every line decodes, and both
	// sessions produced their session brackets.
	events := decodeAOPMessages(decodeEnvelopes(t, &output))
	starts := map[string]bool{}
	ends := map[string]bool{}
	for _, e := range events {
		if e.SessionId != "s1" && e.SessionId != "s2" {
			t.Fatalf("event with foreign session: %+v", e)
		}
		switch e.Payload.(type) {
		case *aop.Event_SessionStarted:
			starts[e.SessionId] = true
		case *aop.Event_SessionEnded:
			ends[e.SessionId] = true
		}
	}
	if !starts["s1"] || !starts["s2"] || !ends["s1"] || !ends["s2"] {
		t.Fatalf("missing session brackets: starts=%v ends=%v", starts, ends)
	}
}

func TestStdioDrainWaitsForInFlightAndQueued(t *testing.T) {
	var output bytes.Buffer
	h := newTestStdioHost(&output)
	prov := newStdioGateProvider()
	newStdioTestSession(t, h, &output, "s1", prov)
	defer h.rt.Close()

	h.accept(runLine(t, "s1", "turn-first", "first"))
	h.accept(runLine(t, "s1", "turn-second", "second"))
	waitForCalls(t, prov, 1, "first run to start")

	drained := make(chan struct{})
	go func() {
		h.drain()
		close(drained)
	}()

	select {
	case <-drained:
		t.Fatal("drain returned while a run was in flight")
	case <-time.After(100 * time.Millisecond):
	}

	close(prov.gate)
	select {
	case <-drained:
	case <-time.After(5 * time.Second):
		t.Fatal("drain did not return after runs completed")
	}
	if got := prov.callCount(); got != 2 {
		t.Fatalf("calls = %d, want 2 (queued message must run before drain returns)", got)
	}
}
