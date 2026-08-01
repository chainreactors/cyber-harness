package web

import (
	"context"
	"errors"
	"sync"
	"testing"
	"time"

	proto "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/pkg/runner"
)

type transactionalConfigStore struct {
	mu         sync.Mutex
	cfg        proto.DistributeConfig
	commitErr  error
	discarded  int
	prepareLog []string
}

func (s *transactionalConfigStore) GetDistributeConfig(context.Context) (string, bool, proto.DistributeConfig, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	return "config.yaml", true, s.cfg, nil
}

func (s *transactionalConfigStore) PrepareDistributeConfig(_ context.Context, cfg proto.DistributeConfig) (*PreparedConfig, error) {
	s.mu.Lock()
	s.prepareLog = append(s.prepareLog, cfg.LLM.Active().Model)
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

func configForModel(model string) proto.DistributeConfig {
	var cfg proto.DistributeConfig
	cfg.LLM.ActiveProfile = "primary"
	cfg.LLM.Providers = []proto.LLMProviderConfig{{ID: "primary", Provider: "openai", Model: model}}
	return cfg
}

func TestSaveConfigBuildFailureKeepsCommittedConfigAndCurrentApp(t *testing.T) {
	store := &transactionalConfigStore{cfg: configForModel("old-model")}
	oldApp, oldClosed := newRecordingApp()
	svc := NewService(ServiceConfig{
		App: oldApp, ConfigStore: store,
		AppFactory: func(_ context.Context, prepared *PreparedConfig) (*runner.App, error) {
			if got := prepared.Config.LLM.Active().Model; got != "new-model" {
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
	if got := committed.LLM.Active().Model; got != "old-model" {
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
			model := prepared.Config.LLM.Active().Model
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
	if got := committed.LLM.Active().Model; got != "second-model" {
		t.Fatalf("final committed model = %q", got)
	}
}
