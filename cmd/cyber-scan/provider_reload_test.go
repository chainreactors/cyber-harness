package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	aop "github.com/chainreactors/cyber/aop"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func TestProfileProviderReloadPreservesActiveRunAndSession(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model  string `json:"model"`
			Stream bool   `json:"stream"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		if request.Model == "quota-exhausted" {
			http.Error(w, `{"error":{"message":"insufficient quota"}}`, http.StatusPaymentRequired)
			return
		}
		if !request.Stream {
			w.Header().Set("Content-Type", "application/json")
			_, _ = fmt.Fprintf(w, `{"choices":[{"message":{"role":"assistant","content":%q}}]}`, request.Model)
			return
		}
		if request.Model == "model-a" {
			close(started)
			select {
			case <-release:
			case <-r.Context().Done():
				return
			}
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"content\":%q},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n", request.Model)
	}))
	defer server.Close()
	defer unblock.Do(func() { close(release) })
	c := minimalConfig(&session.Config{})
	c.Base.DataDir = t.TempDir()
	c.Base.Provider = provider.StartupConfig{Mode: provider.StartupRequired, Config: provider.ProviderConfig{Provider: "openai", BaseURL: server.URL + "/v1", APIKey: "fixture", Model: "model-a", MaxTokens: 256}}
	c.Option.Extensions = cfg.Values{"jev": {"mode": "off"}, "guardrail": {"provider": "none"}}
	p, err := buildCyberScanProfile(c)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	if err := p.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	runtime, err := p.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	conversation, err := runtime.OpenSession(t.Context(), session.SessionOptions{ID: "customer", Messages: []*aop.Message{provider.TextMessage("user", "keep the uploaded HAR context")}})
	if err != nil {
		t.Fatal(err)
	}
	pinned, err := runtime.OpenSession(t.Context(), session.SessionOptions{ID: "pinned"})
	if err != nil {
		t.Fatal(err)
	}
	if err := pinned.SetModel("session-model"); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
	defer cancel()
	first, err := conversation.Run(ctx, session.RunInput{TurnID: "active", Message: agent.TextInput("analyze the upload")})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-started:
	case <-ctx.Done():
		t.Fatal(ctx.Err())
	}
	next := c.Base.Provider.Config
	next.Model = "model-b"
	if err := p.ReloadProvider(ctx, next); err != nil {
		t.Fatal(err)
	}
	if after, err := p.Runtime(); err != nil || after != runtime || conversation.Model() != "model-b" {
		t.Fatalf("provider update replaced runtime or session: %v", err)
	}
	if pinned.Model() != "session-model" {
		t.Fatal("global reload overwrote the session's explicit model")
	}
	unblock.Do(func() { close(release) })
	if result, err := first.Wait(); err != nil || !strings.Contains(result.Output, "model-a") {
		t.Fatalf("active turn did not retain its provider: %v, %v", result, err)
	}
	prior, priorConfig := p.providers.Current()
	priorHealth := p.providers.Health()
	failed := next
	failed.Model = "quota-exhausted"
	if err := p.ReloadProvider(ctx, failed); err == nil {
		t.Fatal("quota failure was accepted")
	}
	if current, config := p.providers.Current(); current != prior || config != priorConfig || p.providers.Health() != priorHealth {
		t.Fatal("failed update damaged the previous provider")
	}
	second, err := conversation.Run(ctx, session.RunInput{TurnID: "next", Message: agent.TextInput("continue")})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := second.Wait(); err != nil || !strings.Contains(result.Output, "model-b") {
		t.Fatalf("next turn did not use the new provider: %v, %v", result, err)
	}
	if messages := conversation.MessagesSnapshot(); len(messages) < 5 || provider.MessageText(messages[0]) != "keep the uploaded HAR context" {
		t.Fatalf("session history was lost: %v", messages)
	}
}

func TestLiveProviderRecoversFailedSession(t *testing.T) {
	if os.Getenv("CYBER_SESSION_RECOVERY_LIVE") != "1" {
		t.Skip("set CYBER_SESSION_RECOVERY_LIVE=1 and CYBER_API_KEY/CYBER_BASE_URL/CYBER_MODEL for a live recovery test")
	}
	next := provider.ProviderConfig{Provider: "openai", BaseURL: os.Getenv("CYBER_BASE_URL"), APIKey: os.Getenv("CYBER_API_KEY"), Model: os.Getenv("CYBER_MODEL"), MaxTokens: 1024, Timeout: 45}
	if next.APIKey == "" || next.BaseURL == "" || next.Model == "" {
		t.Fatal("live recovery test requires provider credentials, endpoint and model")
	}
	quota := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, `{"error":{"message":"insufficient quota"}}`, http.StatusPaymentRequired)
	}))
	defer quota.Close()
	c := minimalConfig(&session.Config{})
	c.Base.DataDir = t.TempDir()
	c.Base.Provider = provider.StartupConfig{Mode: provider.StartupOptional, Config: provider.ProviderConfig{Provider: "openai", BaseURL: quota.URL + "/v1", APIKey: "fixture", Model: "quota-fixture"}}
	c.Option.Extensions = cfg.Values{"jev": {"mode": "off"}, "guardrail": {"provider": "none"}}
	p, err := buildCyberScanProfile(c)
	if err != nil {
		t.Fatal(err)
	}
	defer p.Close(context.Background())
	ctx, cancel := context.WithTimeout(t.Context(), time.Minute)
	defer cancel()
	if err := p.Load(ctx); err != nil {
		t.Fatal(err)
	}
	runtime, err := p.Runtime()
	if err != nil {
		t.Fatal(err)
	}
	marker := fmt.Sprintf("recovery-%d", time.Now().UnixNano())
	conversation, err := runtime.OpenSession(ctx, session.SessionOptions{ID: "live-recovery", Messages: []*aop.Message{provider.TextMessage("user", "Remember this recovery marker for the next request: "+marker)}})
	if err != nil {
		t.Fatal(err)
	}
	failed, err := conversation.Run(ctx, session.RunInput{TurnID: "failed", Message: agent.TextInput("Reply with the recovery marker. Do not call tools.")})
	if err != nil {
		t.Fatal(err)
	}
	if result, err := failed.Wait(); err == nil || result.Stop != agenthooks.StopReasonError {
		t.Fatalf("quota fixture did not fail: %v", err)
	}
	if err := p.ReloadProvider(ctx, next); err != nil {
		t.Fatal(err)
	}
	recovered, err := conversation.Run(ctx, session.RunInput{TurnID: "recovered", Message: agent.TextInput("Reply only with the recovery marker from earlier in this conversation. Do not call tools."), MaxTurns: 2})
	if err != nil {
		t.Fatal(err)
	}
	result, err := recovered.Wait()
	if err != nil || !strings.Contains(result.Output, marker) {
		t.Fatalf("live model failed to recover prior context: %v, %v", result, err)
	}
	if after, err := p.Runtime(); err != nil || after != runtime {
		t.Fatal("live provider update replaced the runtime")
	}
}
