package app

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/extension"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type closeSignal struct {
	once sync.Once
	done chan struct{}
}

func TestNewIsInertUntilLoad(t *testing.T) {
	a := New(Config{SkipEngines: true}, nil, nil)
	if a.ctx != nil || a.Skills != nil || a.Bash != nil || len(a.Commands.Names()) != 0 || len(a.Tools.ToolDefinitions()) != 0 {
		t.Fatal("New exposed initialized application resources before Load")
	}
	if err := a.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func TestCloseCanResumeWaitingAfterContextCancellation(t *testing.T) {
	ready := make(chan struct{})
	a := &App{enginesReady: ready, closeDone: make(chan struct{}), loaded: true}
	ctx, cancel := context.WithCancel(t.Context())
	cancel()
	if err := a.Close(ctx); !errors.Is(err, context.Canceled) {
		t.Fatalf("Close error = %v, want context cancellation", err)
	}
	select {
	case <-a.closeDone:
		t.Fatal("Close released the application before engine assembly stopped")
	default:
	}
	close(ready)
	if err := a.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
}

func (s *closeSignal) Close() { s.once.Do(func() { close(s.done) }) }

func TestCloseReportsRecorderFailureOnceAfterReleasingResources(t *testing.T) {
	a := New(Config{SkipEngines: true}, nil, nil)
	t.Cleanup(func() { _ = a.Close(context.Background()) })
	if err := a.StartRecording(filepath.Join(t.TempDir(), "events.jsonl")); err != nil {
		t.Fatal(err)
	}
	// Feed an invalid event through the actual synchronous recorder subscription.
	a.EventBus.Emit(&aop.Event{})
	closed := make(chan struct{})
	a.Engines = &closeSignal{done: closed}
	err := a.Close(t.Context())
	if err == nil || !strings.Contains(err.Error(), "requires id, session_id and payload") || errors.Is(err, extension.ErrCloseIncomplete) {
		t.Fatalf("Close = %v", err)
	}
	select {
	case <-closed:
	default:
		t.Fatal("terminal recording error prevented resource release")
	}
	if a.Recorder != nil {
		t.Fatal("closed recorder remains owned")
	}
	if err := a.Close(t.Context()); err != nil {
		t.Fatalf("terminal error repeated: %v", err)
	}
}

func TestEmitConcurrentProducersAndReentrantSubscriber(t *testing.T) {
	a := &App{EventBus: eventbus.New[*aop.Event]()}
	var mu sync.Mutex
	seen := make(map[uint64]*aop.Event)
	a.EventBus.Subscribe(func(event *aop.Event) {
		mu.Lock()
		if seen[event.Seq] != nil {
			t.Errorf("duplicate sequence %d", event.Seq)
		}
		seen[event.Seq] = event
		mu.Unlock()
		if event.Id == "outer" {
			a.Emit(&aop.Event{SessionId: "shared", Id: "nested"})
		}
	})
	stamp := timestamppb.Now()
	outer := &aop.Event{SessionId: "shared", Id: "outer", EmittedAt: stamp}
	a.Emit(outer)
	var producers sync.WaitGroup
	for range 32 {
		producers.Add(1)
		go func() {
			defer producers.Done()
			a.Emit(&aop.Event{SessionId: "shared"})
		}()
	}
	producers.Wait()
	if seen[1] != outer || outer.EmittedAt != stamp || outer.Id != "outer" {
		t.Fatal("Emit replaced the original event or its metadata")
	}
	for seq := uint64(1); seq <= 34; seq++ {
		if event := seen[seq]; event == nil || event.EmittedAt == nil || event.Id == "" {
			t.Fatalf("missing event or metadata at sequence %d", seq)
		}
	}
}

func TestReloadProviderPreservesNewerStateAndBuildFailure(t *testing.T) {
	started, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(started)
		<-release
		http.Error(w, "unauthorized", http.StatusUnauthorized)
	}))
	defer srv.Close()
	defer unblock.Do(func() { close(release) })
	a := &App{}
	done := make(chan error, 1)
	go func() {
		_, _, err := a.ReloadProvider(context.Background(), agent.ProviderConfig{
			Provider: "openai", Model: "old", BaseURL: srv.URL + "/v1", APIKey: "test",
		})
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		unblock.Do(func() { close(release) })
		t.Fatal("provider probe did not start")
	}
	_, pending := a.ProviderState()
	if pending.Model != "old" || a.LLMHealth().State != LLMHealthConfigured {
		t.Fatal("valid provider must be installed while the probe is pending")
	}
	newConfig := agent.ProviderConfig{Provider: "openai", Model: "new", BaseURL: srv.URL + "/v1", APIKey: "test"}
	newProvider, err := agent.NewProviderFromResolved(&newConfig)
	if err != nil {
		unblock.Do(func() { close(release) })
		t.Fatal(err)
	}
	a.SetProvider(newProvider, newConfig)
	unblock.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatalf("probe failure rejected valid configuration: %v", err)
	}
	current, config := a.ProviderState()
	if current != newProvider || config.Model != "new" || a.LLMHealth().State != LLMHealthConfigured {
		t.Fatal("late probe overwrote a newer provider or its health")
	}
	if _, _, err := a.ReloadProvider(context.Background(), agent.ProviderConfig{Provider: "unsupported"}); err == nil {
		t.Fatal("unsupported provider was accepted")
	}
	current, config = a.ProviderState()
	if current != newProvider || config.Model != "new" || a.LLMHealth().State != LLMHealthConfigured {
		t.Fatal("failed construction changed the working provider")
	}
}

func TestCloseWaitsForAssemblyBeforeReleasingResources(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ready, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	resourcesClosed := make(chan struct{})
	a := &App{
		ctx: ctx, cancel: cancel, enginesReady: ready, enginesEnabled: true,
		Engines: &closeSignal{done: resourcesClosed}, closeDone: make(chan struct{}), loaded: true,
	}
	if got := a.ScannerState(); got != "loading" {
		t.Fatalf("state = %q", got)
	}
	go func() {
		<-ctx.Done()
		<-release
		close(ready)
	}()
	done := make(chan struct{})
	go func() { _ = a.Close(context.Background()); close(done) }()
	<-ctx.Done()
	select {
	case <-resourcesClosed:
		t.Fatal("resources closed while assembly still owns them")
	default:
	}
	unblock.Do(func() { close(release) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after assembly stopped")
	}
	select {
	case <-resourcesClosed:
	default:
		t.Fatal("App did not close owned resources")
	}
	if err := a.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if err := a.StartRecording(filepath.Join(t.TempDir(), "late.jsonl")); err == nil {
		t.Fatal("closed App reopened its recorder")
	}
	if err := a.SwitchRecording(filepath.Join(t.TempDir(), "late.jsonl")); err == nil {
		t.Fatal("closed App switched its recorder")
	}
}
