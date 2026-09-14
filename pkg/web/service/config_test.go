package service

import (
	"context"
	"errors"
	configpkg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/extension"
	profile "github.com/chainreactors/aiscan/pkg/profile"
	types "github.com/chainreactors/aiscan/pkg/types"
	"sync"
	"testing"
	"time"
)

func TestActivateLLMProfileSelectsByID(t *testing.T) {
	store := &transactionalConfigStore{}
	store.cfg = &types.DistributeConfig{Llm: &types.LLMConfig{
		ActiveProfile: "primary",
		Providers: []*types.LLMProviderConfig{
			{Id: "primary", Name: "Primary", Provider: "openai", Model: "gpt-primary", ApiKey: "key-1"},
			{Id: "fast", Name: "Fast", Provider: "openai", Model: "deepseek-fast", ApiKey: "key-2"},
		},
	}}

	status, err := NewService(ServiceConfig{ConfigStore: store}).ActivateConfig(context.Background(), "fast")
	if err != nil {
		t.Fatal(err)
	}
	// Selection is by id: the list order is untouched and Active() resolves
	// the chosen profile.
	if store.cfg.Llm.ActiveProfile != "fast" || store.cfg.Llm.Providers[0].Id != "primary" {
		t.Fatalf("active profile not switched by id: %+v", store.cfg.Llm)
	}
	if active := configpkg.ActiveLLMProvider(store.cfg.Llm); active.Provider != "openai" || active.Model != "deepseek-fast" || active.ApiKey != "key-2" {
		t.Fatalf("Active() did not resolve the selected profile: %+v", active)
	}
	if status.GetLlm().GetActiveProfile() != "fast" || status.GetLlm().GetActive().GetProvider() != "openai" || status.GetLlm().GetActive().GetModel() != "deepseek-fast" {
		t.Fatalf("view not synchronized: %+v", status.GetLlm())
	}
}

func TestSaveConfigRejectsNegativeModelLimits(t *testing.T) {
	store := &transactionalConfigStore{}
	for _, mutate := range []func(*types.LLMProviderConfig){
		func(p *types.LLMProviderConfig) { p.MaxTokens = -1 },
		func(p *types.LLMProviderConfig) { p.ContextWindow = -1 },
		func(p *types.LLMProviderConfig) { p.Timeout = -1 },
	} {
		profile := &types.LLMProviderConfig{Id: "bad", Model: "test-model"}
		mutate(profile)
		conf := &types.DistributeConfig{Llm: &types.LLMConfig{
			Providers: []*types.LLMProviderConfig{profile},
		}}
		if _, err := NewService(ServiceConfig{ConfigStore: store}).SaveConfig(context.Background(), conf); err == nil {
			t.Fatal("Save() accepted a negative model limit")
		}
		if store.cfg != nil && len(store.cfg.GetLlm().GetProviders()) != 0 {
			t.Fatal("invalid config was persisted")
		}
	}
}

func TestSaveConfigRejectsEmptyProfileModel(t *testing.T) {
	store := &transactionalConfigStore{}
	conf := &types.DistributeConfig{Llm: &types.LLMConfig{
		Providers: []*types.LLMProviderConfig{{Id: "empty", Name: "Empty", Model: "  "}},
	}}

	if _, err := NewService(ServiceConfig{ConfigStore: store}).SaveConfig(context.Background(), conf); err == nil {
		t.Fatal("Save() accepted an empty profile model")
	}
	if store.cfg != nil && len(store.cfg.GetLlm().GetProviders()) != 0 {
		t.Fatal("invalid config was persisted")
	}
}

func TestActivateLLMProfileRejectsEmptyModel(t *testing.T) {
	store := &transactionalConfigStore{}
	store.cfg = &types.DistributeConfig{Llm: &types.LLMConfig{
		ActiveProfile: "primary",
		Providers: []*types.LLMProviderConfig{
			{Id: "primary", Model: "gpt-primary"},
			{Id: "empty", Model: ""},
		},
	}}

	if _, err := NewService(ServiceConfig{ConfigStore: store}).ActivateConfig(context.Background(), "empty"); err == nil {
		t.Fatal("Activate() accepted an empty model")
	}
	if store.cfg.Llm.ActiveProfile != "primary" {
		t.Fatalf("active profile = %q, want primary", store.cfg.Llm.ActiveProfile)
	}
}

type transactionalConfigStore struct {
	mu            sync.Mutex
	cfg           *types.DistributeConfig
	commitErr     error
	discarded     int
	prepareLog    []string
	commitEntered chan struct{}
	releaseCommit <-chan struct{}
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
	if s.commitEntered != nil {
		close(s.commitEntered)
		<-s.releaseCommit
	}
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
	if active := configpkg.ActiveLLMProvider(c.GetLlm()); active != nil {
		return active.Model
	}
	return ""
}

func configForModel(model string) *types.DistributeConfig {
	return &types.DistributeConfig{Llm: &types.LLMConfig{
		ActiveProfile: "primary",
		Providers:     []*types.LLMProviderConfig{{Id: "primary", Provider: "openai", Model: model}},
	}}
}

func TestSaveConfigBuildFailureKeepsCommittedConfigAndSkipsApply(t *testing.T) {
	store := &transactionalConfigStore{cfg: configForModel("old-model")}
	config := NewService(ServiceConfig{
		ConfigStore: store,
		BuildProfile: func(_ context.Context, prepared *PreparedConfig) (*profile.Profile, error) {
			if got := activeModel(prepared.Config); got != "new-model" {
				t.Fatalf("candidate model = %q", got)
			}
			return nil, errors.New("candidate build failed")
		},
	})

	if _, err := config.SaveConfig(context.Background(), configForModel("new-model")); err == nil {
		t.Fatal("Save() succeeded despite candidate build failure")
	}
	_, _, committed, err := store.GetDistributeConfig(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if got := activeModel(committed); got != "old-model" {
		t.Fatalf("committed model = %q, want old-model", got)
	}
	if store.discarded != 1 {
		t.Fatalf("discarded candidates = %d, want 1", store.discarded)
	}
}

func TestSaveConfigCommitFailureClosesCandidate(t *testing.T) {
	store := &transactionalConfigStore{cfg: configForModel("old-model"), commitErr: errors.New("disk full")}
	candidate, _, candidateClosed := newRecordingProfile(t)
	config := NewService(ServiceConfig{
		ConfigStore: store,
		BuildProfile: func(context.Context, *PreparedConfig) (*profile.Profile, error) {
			return candidate, nil
		},
	})

	if _, err := config.SaveConfig(context.Background(), configForModel("new-model")); err == nil {
		t.Fatal("Save() succeeded despite commit failure")
	}
	if !candidateClosed() {
		t.Fatal("candidate app was not closed after commit failure")
	}
}

func TestConfigCloseRetainsCandidateUntilCleanupCompletes(t *testing.T) {
	candidate, _, closed := newRecordingProfile(t)
	config := NewService(ServiceConfig{})
	config.pending = candidate
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if err := config.Close(ctx); !errors.Is(err, extension.ErrCloseIncomplete) || !errors.Is(err, context.Canceled) {
		t.Fatalf("Close = %v", err)
	}
	if config.pending != candidate {
		t.Fatal("failed cleanup lost the candidate")
	}
	if closed() {
		t.Fatal("candidate closed despite canceled cleanup attempt")
	}
	if err := config.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if config.pending != nil {
		t.Fatal("completed candidate retained")
	}
	if !closed() {
		t.Fatal("candidate resources not closed")
	}
}

func TestSaveConfigBuildErrorClosesReturnedPartialCandidate(t *testing.T) {
	candidate, _, closed := newRecordingProfile(t)
	want := errors.New("build failed after loading resources")
	store := &transactionalConfigStore{cfg: configForModel("old-model")}
	config := NewService(ServiceConfig{
		ConfigStore:  store,
		BuildProfile: func(context.Context, *PreparedConfig) (*profile.Profile, error) { return candidate, want },
	})
	if _, err := config.SaveConfig(context.Background(), configForModel("new-model")); err == nil {
		t.Fatal("build failure was lost")
	}
	if !closed() {
		t.Fatal("returned partial candidate was leaked")
	}
	if config.pending != nil {
		t.Fatal("completed candidate remained pending")
	}
}

func TestSaveConfigSerializesConcurrentCandidates(t *testing.T) {
	store := &transactionalConfigStore{cfg: configForModel("old-model")}
	entered := make(chan string, 2)
	releaseFirst := make(chan struct{})
	config := NewService(ServiceConfig{
		ConfigStore: store,
		BuildProfile: func(_ context.Context, prepared *PreparedConfig) (*profile.Profile, error) {
			model := activeModel(prepared.Config)
			entered <- model
			if model == "first-model" {
				<-releaseFirst
			}
			p, _, _ := newRecordingProfile(t)
			return p, nil
		},
	})
	defer config.Close(context.Background())

	firstDone := make(chan error, 1)
	go func() {
		_, err := config.SaveConfig(context.Background(), configForModel("first-model"))
		firstDone <- err
	}()
	if got := <-entered; got != "first-model" {
		t.Fatalf("first candidate = %q", got)
	}

	secondDone := make(chan error, 1)
	go func() {
		_, err := config.SaveConfig(context.Background(), configForModel("second-model"))
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
