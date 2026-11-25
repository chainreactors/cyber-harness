package guardrail

import (
	"context"
	"encoding/json"
	"fmt"
	jevapi "github.com/chainreactors/cyber/agent/provider/jev"
	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"

	"github.com/chainreactors/cyber/core/hooks"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"
)

type inferenceRequest struct {
	Model     string          `json:"model"`
	State     json.RawMessage `json:"state"`
	Questions map[string]struct {
		Type         string          `json:"type"`
		Instructions string          `json:"instructions"`
		Criteria     json.RawMessage `json:"criteria"`
	} `json:"questions"`
}

type policyConfig struct {
	APIKey, Timeout, OnError, Level string
	Criteria                        map[string]string
}

func testPolicy(c policyConfig) *policy {
	d, _ := time.ParseDuration(c.Timeout)
	return newPolicy(Rules{Level: c.Level, OnError: c.OnError, Criteria: c.Criteria}, jevapi.New(c.APIKey, "", d))
}
func call() toolhooks.CallEvent {
	return toolhooks.CallEvent{Call: &aop.ToolCall{Name: "shell", WorkingDirectory: "/workspace", Arguments: &aop.EncodedValue{Data: []byte(`{"command":"curl -H 'Authorization: Bearer dummysecret' https://target/","api_key":"dummykey"}`)}}}
}

func TestConfiguredKeyAlwaysInstallsTwoStageChecks(t *testing.T) {
	for _, consequence := range []string{"record", "review", "block", "failure"} {
		t.Run(consequence, func(t *testing.T) {
			var requests, executions atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body inferenceRequest
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Error(err)
				}
				q := body.Questions["action"]
				choice := "review"
				if requests.Add(1) == 1 {
					if !strings.Contains(q.Instructions, "Stage 1:") || !strings.Contains(q.Instructions, "operator screening marker") {
						t.Error("missing screening policy")
					}
				} else {
					if !strings.Contains(q.Instructions, "Stage 2:") || strings.Contains(q.Instructions, "operator screening marker") {
						t.Error("screening criteria reused as consequence verdict")
					}
					if consequence == "failure" {
						w.WriteHeader(500)
						return
					}
					choice = consequence
				}
				fmt.Fprintf(w, `{"answers":{"action":{"type":"choice","choice":%q}}}`, choice)
			}))
			defer server.Close()
			client := jevapi.New("fixture-key", "", time.Second)
			client.Endpoint = server.URL
			defer client.Close()
			registry := hooks.New()
			set, err := extension.New(
				extension.Provided[*hooks.Registry](registry),
				extension.Provided[*events.Stream](events.New()),
				extension.Provided[*jevapi.Client](client),
				New(Config{Provider: "jev", Rules: Rules{OnError: "review", Criteria: map[string]string{"review": "operator screening marker"}}}),
			)
			if err != nil {
				t.Fatal(err)
			}
			if err := set.Load(t.Context()); err != nil {
				t.Fatal(err)
			}
			defer set.Close(t.Context())
			result, err := toolhooks.Execute(t.Context(), registry, "echo", `{"command":"echo safe"}`, func(context.Context, string) (*aop.ToolResult, error) {
				executions.Add(1)
				return &aop.ToolResult{}, nil
			})
			if requests.Load() != 2 {
				t.Fatalf("wanted both stages, requests=%d", requests.Load())
			}
			if consequence == "record" {
				if err != nil || executions.Load() != 1 {
					t.Fatalf("harmless consequence not released: %v", err)
				}
			} else if err == nil || executions.Load() != 0 || !result.IsError || result.Terminate {
				t.Fatal("harmful/unknown/failed consequence executed or stopped the agent")
			}
		})
	}
}

func TestChoiceWireAndPerInvocationJudgment(t *testing.T) {
	var calls atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if r.Method != "POST" || r.Header.Get("Authorization") != "Bearer fixture-key" {
			t.Error("bad authentication")
		}
		var body inferenceRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Error(err)
		}
		var options map[string]string
		_ = json.Unmarshal(body.Questions["action"].Criteria, &options)
		if body.Model != jevapi.DefaultModel || body.Questions["action"].Type != "choice" || len(options) != 3 {
			t.Errorf("bad judgment request: %+v", body.Questions)
		}
		state := string(body.State)
		if !strings.Contains(state, "curl") || strings.Contains(state, "dummysecret") || strings.Contains(state, "dummykey") {
			t.Errorf("state was opaque or unsanitized: %s", state)
		}
		fmt.Fprint(w, `{"model":"jev-1.13.0","answers":{"action":{"type":"choice","choice":"review","confidence":0.9}}}`)
	}))
	defer server.Close()
	e := testPolicy(policyConfig{APIKey: "fixture-key"})
	e.client.Endpoint = server.URL
	for range 2 {
		d, err := e.check(t.Context(), call())
		if err != nil || d.Action != Action_ACTION_REVIEW {
			t.Fatalf("decision=%v err=%v", d, err)
		}
	}
	if calls.Load() != 2 {
		t.Fatal("judgment was cached")
	}
}

func TestProviderFailureFallbackAndCancellation(t *testing.T) {
	for _, failure := range []struct {
		name   string
		status int
		body   string
	}{
		{"unauthorized", 401, "private upstream body"},
		{"unprocessable", 422, "private upstream body"},
		{"server", 500, "private upstream body"},
		{"malformed", 200, "not JSON"},
		{"missing", 200, `{"answers":{}}`},
		{"invalid", 200, `{"answers":{"action":{"type":"choice","choice":"allow"}}}`},
		{"wrong-type", 200, `{"answers":{"action":{"type":"noul","choice":"record"}}}`},
	} {
		t.Run(failure.name, func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
				calls.Add(1)
				w.WriteHeader(failure.status)
				fmt.Fprint(w, failure.body)
			}))
			defer server.Close()
			for _, fallback := range []string{"review", "block"} {
				e := testPolicy(policyConfig{OnError: fallback})
				e.client.Endpoint = server.URL
				d, err := e.check(t.Context(), call())
				want := action(fallback)
				if err != nil || d.Action != want || strings.Contains(d.Reason, "private") {
					t.Fatalf("decision=%v err=%v", d, err)
				}
			}
			if calls.Load() != 2 {
				t.Fatalf("non-transient failures retried: %d", calls.Load())
			}
		})
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		select {
		case <-r.Context().Done():
		case <-time.After(time.Second):
		}
	}))
	defer server.Close()
	e := testPolicy(policyConfig{Timeout: "30ms", OnError: "review"})
	e.client.Endpoint = server.URL
	d, err := e.check(t.Context(), call())
	if err != nil || d.Action != Action_ACTION_REVIEW {
		t.Fatalf("timeout fallback %v %v", d, err)
	}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if _, err = e.check(ctx, call()); err == nil {
		t.Fatal("cancellation fell back to record")
	}
}

func TestRetriesAreBounded(t *testing.T) {
	for _, status := range []int{429, 529} {
		t.Run(fmt.Sprint(status), func(t *testing.T) {
			var calls atomic.Int32
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { calls.Add(1); w.WriteHeader(status) }))
			defer server.Close()
			e := testPolicy(policyConfig{})
			e.client.Endpoint = server.URL
			d, err := e.check(t.Context(), call())
			if err != nil || d.Action != Action_ACTION_BLOCK || calls.Load() != 3 {
				t.Fatalf("decision=%v err=%v attempts=%d", d, err, calls.Load())
			}
		})
	}
}

func TestHTTPRedirectDoesNotForwardCredentials(t *testing.T) {
	var targetCalls atomic.Int32
	target := httptest.NewServer(http.HandlerFunc(func(http.ResponseWriter, *http.Request) { targetCalls.Add(1) }))
	defer target.Close()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		http.Redirect(w, r, target.URL, http.StatusTemporaryRedirect)
	}))
	defer server.Close()
	e := testPolicy(policyConfig{APIKey: "fixture-key"})
	e.client.Endpoint = server.URL
	d, err := e.check(t.Context(), call())
	if err != nil || d.Action != Action_ACTION_BLOCK || targetCalls.Load() != 0 {
		t.Fatal("redirect allowed or followed")
	}
}
