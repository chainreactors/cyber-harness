//go:build sqlite

package main

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	jevapi "github.com/chainreactors/cyber/agent/provider/jev"
	agentsession "github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/aop"
	jevext "github.com/chainreactors/cyber/exts/jev"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"google.golang.org/protobuf/encoding/protojson"
	"google.golang.org/protobuf/proto"
)

type profileJEVTransport struct{}

func (profileJEVTransport) RoundTrip(r *http.Request) (*http.Response, error) {
	if r.URL.String() != jevapi.Endpoint {
		return nil, fmt.Errorf("fixture rejects external request")
	}
	var request struct {
		State     json.RawMessage `json:"state"`
		Questions map[string]struct {
			Criteria json.RawMessage `json:"criteria"`
		} `json:"questions"`
	}
	if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
		return nil, err
	}
	answers := map[string]map[string]string{}
	for id, q := range request.Questions {
		var options map[string]string
		_ = json.Unmarshal(q.Criteria, &options)
		choice := "defer"
		switch {
		case id == "entry":
			for key := range options {
				if strings.HasPrefix(key, "r") {
					choice = key
					break
				}
			}
		case id == "input" || id == "binding" || id == "completion":
			choice = "accept"
		case strings.HasPrefix(id, "claim"):
			choice = "new"
			for key := range options {
				if strings.HasPrefix(key, "c") {
					choice = key
					break
				}
			}
		case strings.HasPrefix(id, "compile") || strings.HasPrefix(id, "coverage"):
			if bytes.Contains(request.State, []byte(`"reflex":`)) || (bytes.Contains(request.State, []byte(`call_id`)) && bytes.Contains(request.State, []byte(`"native_access":"read"`))) {
				choice = "compile"
			}
		case strings.HasPrefix(id, "c"):
			choice = "include"
		}
		answers[id] = map[string]string{"type": "choice", "choice": choice}
	}
	data, _ := json.Marshal(map[string]any{"answers": answers, "usage": map[string]int{"input_tokens": 10, "output_tokens": 1}})
	return &http.Response{StatusCode: 200, Header: http.Header{}, Body: io.NopCloser(bytes.NewReader(data)), Request: r}, nil
}

// This provider is deliberately simulated: the integration proves profile,
// streaming, publication and protocol events, not paid model accuracy.
type profileFlowProvider struct {
	mu                              sync.Mutex
	ordinary                        map[string]int
	compiler, composition, streamed int
	artifact                        string
}

func (*profileFlowProvider) Name() string { return "profile-fixture" }
func (p *profileFlowProvider) ChatCompletion(_ context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()
	var message *aop.Message
	switch {
	case req.Purpose == "compilation":
		p.compiler++
		message = provider.TextMessage("assistant", p.artifact)
	case len(req.Messages) > 0 && strings.HasPrefix(provider.MessageText(req.Messages[0]), "Describe reusable semantic judgments as Claims"):
		message = provider.TextMessage("assistant", `{"claims":[{"type":"noul","context":"Inspect currently open browser sessions and report only the current native evidence."}]}`)
	case req.Purpose == "composition":
		if len(req.Tools) != 0 {
			return nil, fmt.Errorf("composition retained executable tools")
		}
		p.composition++
		message = provider.TextMessage("assistant", "Current sessions verified from native evidence.")
	default:
		p.ordinary[req.SessionID]++
		if p.ordinary[req.SessionID] == 1 {
			message = &aop.Message{Role: "assistant", Content: []*aop.Content{{Value: &aop.Content_ToolCall{ToolCall: &aop.ToolCall{Id: aop.EnvelopeID(), Name: "bash", Arguments: &aop.EncodedValue{MediaType: aop.JSONMediaType, Data: []byte(`{"command":"playwright sessions"}`)}}}}}}
		} else {
			message = provider.TextMessage("assistant", "Current sessions verified from native evidence.")
		}
	}
	return &provider.ChatCompletionResponse{Choices: []provider.Choice{{Message: message, FinishReason: "stop"}}, Usage: &aop.TokenUsage{InputTokens: 20, OutputTokens: 2, TotalTokens: 22}}, nil
}
func (p *profileFlowProvider) ChatCompletionStream(ctx context.Context, req *provider.ChatCompletionRequest) (<-chan provider.ChatCompletionStreamEvent, error) {
	response, err := p.ChatCompletion(ctx, req)
	if err != nil {
		return nil, err
	}
	p.mu.Lock()
	p.streamed++
	p.mu.Unlock()
	out := make(chan provider.ChatCompletionStreamEvent, 4)
	message := response.Choices[0].Message
	for _, part := range message.Content {
		if call := part.GetToolCall(); call != nil {
			out <- provider.ChatCompletionStreamEvent{Role: "assistant", ToolDeltas: []*aop.ToolCallDelta{{Index: 0, CallId: call.Id, Name: call.Name, Arguments: call.GetArguments().GetData()}}}
		} else {
			out <- provider.ChatCompletionStreamEvent{Role: "assistant", MessageDelta: &aop.MessageDelta{Value: &aop.MessageDelta_Text{Text: part.GetText().GetText()}}}
		}
	}
	out <- provider.ChatCompletionStreamEvent{Usage: response.Usage, Done: true, FinishReason: "stop"}
	close(out)
	return out, nil
}

func TestJEVProfileStreamingCompilationRuntimeAndProtocolEvents(t *testing.T) {
	t.Setenv("TYPESAFE_API_KEY", "")
	original := http.DefaultTransport
	http.DefaultTransport = profileJEVTransport{}
	defer func() { http.DefaultTransport = original }()
	directory := t.TempDir()
	c := minimalConfig(&agentsession.Config{})
	c.Base.DataDir = t.TempDir()
	c.Option.Extensions = cfg.Values{"jev": {"mode": "auto", "api_key": "fixture", "directory": directory}, "guardrail": {"provider": "none"}}
	p, err := buildCyberScanProfile(c)
	if err != nil {
		t.Fatal(err)
	}
	if err = p.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	source := `js:function(context,args){const seen=context.history.filter(function(r){return r.name==="bash"&&r.arguments&&r.arguments.command==="playwright sessions"&&!r.is_error;});const r=seen.length?seen[seen.length-1]:execute({name:"bash",arguments:{command:command("playwright",["sessions"])},read:true});if(r.is_error)return{defer:"native read failed"};return{report:{evidence:r.call_id,path:["text"]}};}`
	artifact, _ := json.Marshal(map[string]any{"when": "Inspect currently open browser sessions", "decide": "Report current native session evidence", "steps": map[string]any{}, "observe": source, "arguments": map[string]any{}})
	model := &profileFlowProvider{ordinary: map[string]int{}, artifact: string(artifact)}
	p.providers.Set(model, provider.ProviderConfig{Model: "fixture", MaxTokens: 1024})
	mux := aop.NewNamespaceMux(t.Context())
	defer mux.Close(context.Background())
	if err := p.RegisterNamespaces(mux); err != nil {
		t.Fatal(err)
	}
	query := func(message *jevext.ProtocolMessage) *jevext.ProtocolMessage {
		var response proto.Message
		handled, err := mux.Dispatch(aop.Reply("", message), func(e *aop.Envelope) error { var err error; response, err = aop.Unwrap(e); return err })
		if err != nil || !handled {
			t.Fatalf("namespace query: %v", err)
		}
		value, ok := response.(*jevext.ProtocolMessage)
		if !ok {
			t.Fatalf("unexpected namespace response %T", response)
		}
		return value
	}
	var eventMu sync.Mutex
	events := []*aop.Event{}
	sub := p.events.Observe(func(ev *aop.Event) {
		eventMu.Lock()
		defer eventMu.Unlock()
		events = append(events, proto.Clone(ev).(*aop.Event))
	})
	defer sub.Close(context.Background())
	for _, sessionID := range []string{"profile-training-1", "profile-training-2", "profile-reuse"} {
		session, err := p.runtime.EnsureSession(agentsession.SessionOptions{ID: sessionID})
		if err != nil {
			t.Fatal(err)
		}
		run, err := session.Run(t.Context(), agentsession.RunInput{Message: agent.TextInput("List current browser sessions from native evidence."), MaxTurns: 4})
		if err != nil {
			t.Fatal(err)
		}
		result, err := run.Wait()
		if err != nil || result == nil || !strings.Contains(result.Output, "Current sessions verified") {
			t.Fatalf("streaming run: %v", err)
		}
		idle := query(&jevext.ProtocolMessage{Message: &jevext.ProtocolMessage_WaitIdle{WaitIdle: &jevext.WaitIdleRequest{SessionId: sessionID, TimeoutMs: 5000}}})
		if !idle.GetIdle().GetSettled() {
			t.Fatalf("background unsettled: %v", idle)
		}
		library := query(&jevext.ProtocolMessage{Message: &jevext.ProtocolMessage_Request{Request: &jevext.GetLibraryRequest{SessionId: sessionID}}})
		if len(library.GetLibrary().GetReflexes()) != 1 || len(library.GetLibrary().GetClaims()) != 1 {
			data, _ := os.ReadFile(filepath.Join(directory, "decisions.jsonl"))
			t.Logf("audit: %s", data)
			t.Fatalf("compiled native source not published: %v", library)
		}
	}
	model.mu.Lock()
	if model.compiler != 1 || model.composition != 2 || model.streamed < 3 || model.ordinary["profile-training-2"] != 0 || model.ordinary["profile-reuse"] != 0 {
		t.Errorf("compiler=%d composer=%d streaming=%d ordinary reuse=%d", model.compiler, model.composition, model.streamed, model.ordinary["profile-reuse"])
	}
	model.mu.Unlock()
	eventMu.Lock()
	captured := append([]*aop.Event(nil), events...)
	eventMu.Unlock()
	counts := map[string]int{}
	for _, ev := range captured {
		if !strings.HasPrefix(ev.SessionId, "profile-training-") && ev.SessionId != "profile-reuse" {
			continue
		}
		raw, err := protojson.Marshal(ev)
		if err != nil {
			t.Fatal(err)
		}
		replayed := new(aop.Event)
		if err := protojson.Unmarshal(raw, replayed); err != nil {
			t.Fatalf("protocol event replay changed: %v", err)
		}
		// Any payloads contain maps whose binary field order can change when
		// rebuilt from JSON. Compare the wire JSON that the UI consumes.
		replayJSON, err := protojson.Marshal(replayed)
		if err != nil {
			t.Fatal(err)
		}
		var before, after any
		if err := json.Unmarshal(raw, &before); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(replayJSON, &after); err != nil || !reflect.DeepEqual(before, after) {
			t.Fatalf("protocol event replay changed: %v", err)
		}
		var runtime jevext.RuntimeEvent
		if ev.GetExtension() != nil && ev.GetExtension().UnmarshalTo(&runtime) == nil {
			if runtime.GetLibraryChange().GetState() == "reflex_published" {
				counts["publication"]++
			}
			if runtime.GetDispatch() != nil {
				counts["dispatch"]++
			}
			if runtime.GetHandoff().GetReason() == "report" {
				counts["report"]++
			}
		}
	}
	if counts["publication"] != 1 || counts["dispatch"] != 2 || counts["report"] != 2 {
		t.Fatalf("missing mechanism flow: %v", counts)
	}
	if path := os.Getenv("JEV_PROFILE_FLOW_EVENTS"); path != "" {
		deliveries := []map[string]json.RawMessage{}
		for _, ev := range captured {
			raw, err := protojson.Marshal(ev)
			if err != nil {
				t.Fatal(err)
			}
			deliveries = append(deliveries, map[string]json.RawMessage{"event": raw})
		}
		data, _ := json.MarshalIndent(deliveries, "", "  ")
		if err := os.WriteFile(path, data, 0600); err != nil {
			t.Fatal(err)
		}
	}
}
