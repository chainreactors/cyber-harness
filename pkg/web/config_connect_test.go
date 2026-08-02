package web

import (
	"context"
	"errors"
	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/runner"
	types "github.com/chainreactors/aiscan/pkg/types"
	"sync"
	"testing"
	"time"
)

func TestActivateLLMProfileSelectsByID(t *testing.T) {
	store := &fakeConfigStore{}
	store.cfg = &types.DistributeConfig{Llm: &types.LLMConfig{
		ActiveProfile: "primary",
		Providers: []*types.LLMProviderConfig{
			{Id: "primary", Name: "Primary", Provider: "openai", Model: "gpt-primary", ApiKey: "key-1"},
			{Id: "fast", Name: "Fast", Provider: "openai", Model: "deepseek-fast", ApiKey: "key-2"},
		},
	}}
	service := NewService(ServiceConfig{ConfigStore: store})

	status, err := service.ActivateLLMProfile(context.Background(), "fast")
	if err != nil {
		t.Fatal(err)
	}
	// Selection is by id: the list order is untouched and Active() resolves
	// the chosen profile.
	if store.cfg.Llm.ActiveProfile != "fast" || store.cfg.Llm.Providers[0].Id != "primary" {
		t.Fatalf("active profile not switched by id: %+v", store.cfg.Llm)
	}
	if active := cfg.ActiveLLMProvider(store.cfg.Llm); active.Provider != "openai" || active.Model != "deepseek-fast" || active.ApiKey != "key-2" {
		t.Fatalf("Active() did not resolve the selected profile: %+v", active)
	}
	if status.GetLlm().GetActiveProfile() != "fast" || status.GetLlm().GetActive().GetProvider() != "openai" || status.GetLlm().GetActive().GetModel() != "deepseek-fast" {
		t.Fatalf("view not synchronized: %+v", status.GetLlm())
	}
}

func TestConfigStatusIncludesModelLimits(t *testing.T) {
	conf := &types.DistributeConfig{Llm: &types.LLMConfig{
		ActiveProfile: "large",
		Providers: []*types.LLMProviderConfig{{
			Id: "large", Provider: "anthropic", Model: "glm-5.2[1m]",
			MaxTokens: 32768, ContextWindow: 1000000,
		}},
	}}
	view := ConfigViewFromDistribute(conf, "aiscan.yaml", true)
	if view.GetLlm().GetActive().GetMaxTokens() != 32768 || view.GetLlm().GetActive().GetContextWindow() != 1000000 {
		t.Fatalf("active limits missing from view: %+v", view.GetLlm())
	}
	if len(view.GetLlm().GetProviders()) != 1 || view.GetLlm().GetProviders()[0].GetMaxTokens() != 32768 || view.GetLlm().GetProviders()[0].GetContextWindow() != 1000000 {
		t.Fatalf("profile limits missing from view: %+v", view.GetLlm().GetProviders())
	}
}

func TestSaveConfigRejectsNegativeModelLimits(t *testing.T) {
	store := &fakeConfigStore{}
	service := NewService(ServiceConfig{ConfigStore: store})
	for _, mutate := range []func(*types.LLMProviderConfig){
		func(p *types.LLMProviderConfig) { p.MaxTokens = -1 },
		func(p *types.LLMProviderConfig) { p.ContextWindow = -1 },
	} {
		profile := &types.LLMProviderConfig{Id: "bad", Model: "test-model"}
		mutate(profile)
		conf := &types.DistributeConfig{Llm: &types.LLMConfig{
			Providers: []*types.LLMProviderConfig{profile},
		}}
		if _, err := service.SaveConfig(context.Background(), conf); err == nil {
			t.Fatal("SaveConfig() accepted a negative model limit")
		}
		if store.cfg != nil && len(store.cfg.GetLlm().GetProviders()) != 0 {
			t.Fatal("invalid config was persisted")
		}
	}
}

func TestSaveConfigRejectsEmptyProfileModel(t *testing.T) {
	store := &fakeConfigStore{}
	service := NewService(ServiceConfig{ConfigStore: store})
	conf := &types.DistributeConfig{Llm: &types.LLMConfig{
		Providers: []*types.LLMProviderConfig{{Id: "empty", Name: "Empty", Model: "  "}},
	}}

	if _, err := service.SaveConfig(context.Background(), conf); err == nil {
		t.Fatal("SaveConfig() accepted an empty profile model")
	}
	if store.cfg != nil && len(store.cfg.GetLlm().GetProviders()) != 0 {
		t.Fatal("invalid config was persisted")
	}
}

func TestActivateLLMProfileRejectsEmptyModel(t *testing.T) {
	store := &fakeConfigStore{}
	store.cfg = &types.DistributeConfig{Llm: &types.LLMConfig{
		ActiveProfile: "primary",
		Providers: []*types.LLMProviderConfig{
			{Id: "primary", Model: "gpt-primary"},
			{Id: "empty", Model: ""},
		},
	}}
	service := NewService(ServiceConfig{ConfigStore: store})

	if _, err := service.ActivateLLMProfile(context.Background(), "empty"); err == nil {
		t.Fatal("ActivateLLMProfile() accepted an empty model")
	}
	if store.cfg.Llm.ActiveProfile != "primary" {
		t.Fatalf("active profile = %q, want primary", store.cfg.Llm.ActiveProfile)
	}
}

func newFakeAgent(nodeID string, buffer int) *remoteAgent {
	return &remoteAgent{
		nodeState: newNodeState(),
		nodeID:    nodeID, name: nodeID, sendCh: make(chan *aop.Envelope, buffer),
		done: make(chan struct{}),
	}
}

func TestBroadcastConfigReloadUsesApplicationFIFO(t *testing.T) {
	pool := NewAgentPool(nil)
	agent := newFakeAgent("agent", 1)
	pool.register(agent)
	config := &types.DistributeConfig{Llm: &types.LLMConfig{ActiveProfile: "primary"}}

	if n := pool.BroadcastConfigReload(config); n != 1 {
		t.Fatalf("notified = %d, want 1", n)
	}
	envelope := <-agent.sendCh
	message, err := aop.Unwrap(envelope)
	if err != nil {
		t.Fatal(err)
	}
	reload, ok := message.(*types.ReloadProtocolMessage)
	if !ok || reload.GetRequest().GetConfig().GetLlm().GetActiveProfile() != "primary" {
		t.Fatalf("reload = %T %+v", message, message)
	}
}

func TestBroadcastConfigReloadWaitsInFIFOOrder(t *testing.T) {
	pool := NewAgentPool(nil)
	agent := newFakeAgent("busy", 1)
	cancel := aop.MustWrap("cancel", "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_CancelOperation{CancelOperation: &aop.CancelOperation{TargetId: "task-1"}}})
	agent.sendCh <- cancel
	pool.register(agent)

	done := make(chan int, 1)
	go func() { done <- pool.BroadcastConfigReload(&types.DistributeConfig{}) }()
	select {
	case <-done:
		t.Fatal("reload bypassed the full FIFO")
	case <-time.After(50 * time.Millisecond):
	}
	if first := <-agent.sendCh; first.Id != "cancel" {
		t.Fatalf("first envelope = %+v", first)
	}
	if notified := <-done; notified != 1 {
		t.Fatalf("notified = %d", notified)
	}
	message, _ := aop.Unwrap(<-agent.sendCh)
	if reload, ok := message.(*types.ReloadProtocolMessage); !ok || reload.GetRequest() == nil {
		t.Fatalf("second message = %T", message)
	}
}

func TestHandleAgentStatusUpdate(t *testing.T) {
	pool := NewAgentPool(nil)
	agent := newFakeAgent("n1", 1)
	agent.runtime = &aop.AgentRuntimeInfo{Pid: 4242, Hostname: "local-1"}
	agent.status = &aop.AgentStatus{Provider: "anthropic", Model: "old-model"}
	pool.register(agent)

	pool.handleAgentEnvelope(agent, aop.MustWrap("status", "", &aop.ProtocolMessage{Message: &aop.ProtocolMessage_AgentStatus{AgentStatus: &aop.AgentStatus{
		Provider: "anthropic", Model: "glm-5.2", Bound: true,
	}}}))

	view := agent.view()
	if view.GetStatus().GetModel() != "glm-5.2" || view.GetStatus().GetProvider() != "anthropic" {
		t.Fatalf("status = %+v", view.GetStatus())
	}
	if runtime := view.GetHello().GetRuntime(); runtime.GetHostname() != "local-1" || runtime.GetPid() != 4242 {
		t.Fatalf("runtime clobbered: %+v", runtime)
	}
}

func TestHandleConfigReloadResultUpdatesAgentStatus(t *testing.T) {
	pool := NewAgentPool(nil)
	agent := newFakeAgent("n1", 1)
	agent.status = &aop.AgentStatus{Provider: "openai", Model: "old-model"}
	pool.register(agent)

	pool.handleAgentEnvelope(agent, aop.MustWrap("reload-result", "reload", &types.ReloadProtocolMessage{Message: &types.ReloadProtocolMessage_Result{Result: &types.ReloadResult{
		Ok: true, Provider: "openai", Model: "deepseek-v4-pro",
	}}}))
	if got := agent.view().GetStatus(); got.GetProvider() != "openai" || got.GetModel() != "deepseek-v4-pro" || got.GetConfigError() != "" {
		t.Fatalf("unexpected config result status: %+v", got)
	}

	pool.handleAgentEnvelope(agent, aop.MustWrap("reload-error", "reload", &types.ReloadProtocolMessage{Message: &types.ReloadProtocolMessage_Result{Result: &types.ReloadResult{
		Ok: false, Error: "invalid API key",
	}}}))
	if got := agent.view().GetStatus(); got.GetConfigError() != "invalid API key" {
		t.Fatalf("config error = %q", got.GetConfigError())
	}
}

type transactionalConfigStore struct {
	mu         sync.Mutex
	cfg        *types.DistributeConfig
	commitErr  error
	discarded  int
	prepareLog []string
}

func (s *transactionalConfigStore) GetDistributeConfig(context.Context) (string, bool, *types.DistributeConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return "config.yaml", true, s.cfg, nil
}

func (s *transactionalConfigStore) PrepareDistributeConfig(_ context.Context, cfg *types.DistributeConfig) (*PreparedConfig, error) {
	s.mu.Lock()
	s.prepareLog = append(s.prepareLog, activeModel(cfg))
	s.mu.Unlock()
	return &PreparedConfig{Config: cfg, TargetPath: "config.yaml"}, nil
}

func (s *transactionalConfigStore) CommitDistributeConfig(_ context.Context, prepared *PreparedConfig) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.commitErr != nil {
		return s.commitErr
	}
	s.cfg = prepared.Config
	return nil
}

func (s *transactionalConfigStore) DiscardDistributeConfig(*PreparedConfig) {
	s.mu.Lock()
	s.discarded++
	s.mu.Unlock()
}

func activeModel(c *types.DistributeConfig) string {
	if active := cfg.ActiveLLMProvider(c.GetLlm()); active != nil {
		return active.Model
	}
	return ""
}

type recordingCloser struct {
	once sync.Once
	done chan struct{}
}

func newRecordingApp() (*runner.App, <-chan struct{}) {
	closer := &recordingCloser{done: make(chan struct{})}
	return &runner.App{Engines: closer}, closer.done
}

func (c *recordingCloser) Close() {
	c.once.Do(func() { close(c.done) })
}

func configForModel(model string) *types.DistributeConfig {
	return &types.DistributeConfig{Llm: &types.LLMConfig{
		ActiveProfile: "primary",
		Providers:     []*types.LLMProviderConfig{{Id: "primary", Provider: "openai", Model: model}},
	}}
}

func TestSaveConfigBuildFailureKeepsCommittedConfigAndCurrentApp(t *testing.T) {
	store := &transactionalConfigStore{cfg: configForModel("old-model")}
	oldApp, oldClosed := newRecordingApp()
	svc := NewService(ServiceConfig{
		App: oldApp, ConfigStore: store,
		AppFactory: func(_ context.Context, prepared *PreparedConfig) (*runner.App, error) {
			if got := activeModel(prepared.Config); got != "new-model" {
				t.Fatalf("candidate model = %q", got)
			}
			return nil, errors.New("candidate build failed")
		},
	})

	if _, err := svc.SaveConfig(context.Background(), configForModel("new-model")); err == nil {
		t.Fatal("SaveConfig() succeeded despite candidate build failure")
	}
	_, _, committed, err := store.GetDistributeConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := activeModel(committed); got != "old-model" {
		t.Fatalf("committed model = %q, want old-model", got)
	}
	app, release := svc.acquireApp()
	defer release()
	if app != oldApp {
		t.Fatal("build failure replaced the current app")
	}
	select {
	case <-oldClosed:
		t.Fatal("build failure closed the current app")
	default:
	}
	if store.discarded != 1 {
		t.Fatalf("discarded candidates = %d, want 1", store.discarded)
	}
}

func TestSaveConfigCommitFailureClosesCandidateAndKeepsCurrentApp(t *testing.T) {
	store := &transactionalConfigStore{cfg: configForModel("old-model"), commitErr: errors.New("disk full")}
	oldApp, oldClosed := newRecordingApp()
	candidateApp, candidateClosed := newRecordingApp()
	svc := NewService(ServiceConfig{
		App: oldApp, ConfigStore: store,
		AppFactory: func(context.Context, *PreparedConfig) (*runner.App, error) {
			return candidateApp, nil
		},
	})

	if _, err := svc.SaveConfig(context.Background(), configForModel("new-model")); err == nil {
		t.Fatal("SaveConfig() succeeded despite commit failure")
	}
	select {
	case <-candidateClosed:
	default:
		t.Fatal("candidate app was not closed after commit failure")
	}
	select {
	case <-oldClosed:
		t.Fatal("commit failure closed the current app")
	default:
	}
	app, release := svc.acquireApp()
	defer release()
	if app != oldApp {
		t.Fatal("commit failure replaced the current app")
	}
}

func TestSwapAppDefersOldCloseUntilActiveLeaseReleases(t *testing.T) {
	oldApp, oldClosed := newRecordingApp()
	nextApp, _ := newRecordingApp()
	svc := NewService(ServiceConfig{App: oldApp})

	leased, release := svc.acquireApp()
	if leased != oldApp {
		t.Fatal("acquireApp() returned the wrong app")
	}
	svc.swapApp(nextApp)
	select {
	case <-oldClosed:
		t.Fatal("old app closed while a scan still held a lease")
	default:
	}
	release()
	select {
	case <-oldClosed:
	default:
		t.Fatal("old app remained open after the final lease released")
	}
}

func TestSaveConfigSerializesConcurrentCandidates(t *testing.T) {
	store := &transactionalConfigStore{cfg: configForModel("old-model")}
	oldApp, _ := newRecordingApp()
	entered := make(chan string, 2)
	releaseFirst := make(chan struct{})
	svc := NewService(ServiceConfig{
		App: oldApp, ConfigStore: store,
		AppFactory: func(_ context.Context, prepared *PreparedConfig) (*runner.App, error) {
			model := activeModel(prepared.Config)
			entered <- model
			if model == "first-model" {
				<-releaseFirst
			}
			app, _ := newRecordingApp()
			return app, nil
		},
	})
	t.Cleanup(svc.Close)

	firstDone := make(chan error, 1)
	go func() {
		_, err := svc.SaveConfig(context.Background(), configForModel("first-model"))
		firstDone <- err
	}()
	if got := <-entered; got != "first-model" {
		t.Fatalf("first candidate = %q", got)
	}

	secondDone := make(chan error, 1)
	go func() {
		_, err := svc.SaveConfig(context.Background(), configForModel("second-model"))
		secondDone <- err
	}()
	select {
	case model := <-entered:
		t.Fatalf("second candidate %q entered before first commit", model)
	case <-time.After(50 * time.Millisecond):
	}

	close(releaseFirst)
	if err := <-firstDone; err != nil {
		t.Fatal(err)
	}
	if got := <-entered; got != "second-model" {
		t.Fatalf("second candidate = %q", got)
	}
	if err := <-secondDone; err != nil {
		t.Fatal(err)
	}
	_, _, committed, err := store.GetDistributeConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := activeModel(committed); got != "second-model" {
		t.Fatalf("final committed model = %q", got)
	}
}
