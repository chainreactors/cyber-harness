package app

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/pkg/commands"
	"google.golang.org/protobuf/types/known/timestamppb"
)

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

func TestCloseWaitsForAssemblyBeforeReleasingCommands(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	ready, release := make(chan struct{}), make(chan struct{})
	var unblock sync.Once
	defer unblock.Do(func() { close(release) })
	commandsClosed := make(chan struct{})
	reg := commands.NewRegistry()
	reg.Register(commands.Command{Name: "inert", Close: func() { close(commandsClosed) }}, "test")
	a := &App{ctx: ctx, cancel: cancel, enginesReady: ready, enginesEnabled: true, Commands: reg}
	if got := a.ScannerState(); got != "loading" {
		t.Fatalf("state = %q", got)
	}
	go func() {
		<-ctx.Done()
		<-release
		close(ready)
	}()
	done := make(chan struct{})
	go func() { a.Close(); close(done) }()
	<-ctx.Done()
	select {
	case <-commandsClosed:
		t.Fatal("commands closed while assembly still owns them")
	default:
	}
	unblock.Do(func() { close(release) })
	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("Close did not finish after assembly stopped")
	}
	select {
	case <-commandsClosed:
	default:
		t.Fatal("App did not close owned commands")
	}
	a.Close()
	if err := a.StartRecording(filepath.Join(t.TempDir(), "late.jsonl")); err == nil {
		t.Fatal("closed App reopened its recorder")
	}
	if err := a.SwitchRecording(filepath.Join(t.TempDir(), "late.jsonl")); err == nil {
		t.Fatal("closed App switched its recorder")
	}
}

func TestIOARetryOutlivesCallerAndStopsWithApp(t *testing.T) {
	var calls atomic.Int32
	retrying, canceled := make(chan struct{}), make(chan struct{})
	stopServer := make(chan struct{})
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		_, _ = io.Copy(io.Discard, r.Body)
		if calls.Add(1) == 1 {
			http.Error(w, "try later", http.StatusServiceUnavailable)
			return
		}
		close(retrying)
		select {
		case <-r.Context().Done():
			close(canceled)
		case <-stopServer:
		}
	}))
	defer srv.Close()
	defer close(stopServer)
	ctx, cancel := context.WithCancel(context.Background())
	a := &App{ctx: ctx, cancel: cancel}
	defer a.Close()
	caller, cancelCaller := context.WithCancel(context.Background())
	defer cancelCaller()
	if err := a.InitIOA(caller, IOAConfig{URL: srv.URL, AutoRegister: true}); err != nil {
		t.Fatal(err)
	}
	cancelCaller()
	select {
	case <-retrying:
	case <-time.After(5 * time.Second):
		t.Fatal("caller cancellation stopped application registration retries")
	}
	a.Close()
	select {
	case <-canceled:
	case <-time.After(time.Second):
		t.Fatal("App close did not cancel the in-flight registration")
	}
	if err := a.InitIOA(context.Background(), IOAConfig{URL: srv.URL}); err == nil {
		t.Fatal("closed App admitted another registration")
	}
}
