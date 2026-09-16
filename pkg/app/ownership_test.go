package app

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	aop "github.com/chainreactors/cyber/aop"
	coreevents "github.com/chainreactors/cyber/core/events"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestNewDoesNotPopulateBorrowedRegistries(t *testing.T) {
	a := newTestApp(t, nil, Dependencies{})
	if len(a.Skills.All()) != 0 || a.Bash != nil || len(a.Commands.Names()) != 0 || len(a.Tools.ToolDefinitions()) != 0 {
		t.Fatal("New populated profile-owned resources")
	}
}

func TestPublishConcurrentProducersAndReentrantObserver(t *testing.T) {
	stream := coreevents.New()
	a := &App{events: stream}
	var mu sync.Mutex
	seen := make(map[uint64]*aop.Event)
	a.ObserveEvents(coreevents.ObserverFunc(func(event *aop.Event) {
		mu.Lock()
		if seen[event.Seq] != nil {
			t.Errorf("duplicate sequence %d", event.Seq)
		}
		seen[event.Seq] = event
		mu.Unlock()
		if event.Id == "outer" {
			a.Publish(&aop.Event{SessionId: "shared", Id: "nested"})
		}
	}))
	stamp := timestamppb.Now()
	outer := &aop.Event{SessionId: "shared", Id: "outer", EmittedAt: stamp}
	a.Publish(outer)
	var producers sync.WaitGroup
	for range 32 {
		producers.Add(1)
		go func() {
			defer producers.Done()
			a.Publish(&aop.Event{SessionId: "shared"})
		}()
	}
	producers.Wait()
	if seen[1] != outer || outer.EmittedAt != stamp || outer.Id != "outer" {
		t.Fatal("Publish replaced the original event or its metadata")
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
	if pending.Model != "old" || a.ProviderHealth().State != provider.HealthConfigured {
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
	if current != newProvider || config.Model != "new" || a.ProviderHealth().State != provider.HealthConfigured {
		t.Fatal("late probe overwrote a newer provider or its health")
	}
	if _, _, err := a.ReloadProvider(context.Background(), agent.ProviderConfig{Provider: "unsupported"}); err == nil {
		t.Fatal("unsupported provider was accepted")
	}
	current, config = a.ProviderState()
	if current != newProvider || config.Model != "new" || a.ProviderHealth().State != provider.HealthConfigured {
		t.Fatal("failed construction changed the working provider")
	}
}
