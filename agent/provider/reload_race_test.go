package provider_test

import (
	"context"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
)

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
	a := &provider.State{}
	done := make(chan error, 1)
	go func() {
		_, _, err := a.Reload(context.Background(), agent.ProviderConfig{
			Provider: "openai", Model: "old", BaseURL: srv.URL + "/v1", APIKey: "test",
		}, nil)
		done <- err
	}()
	select {
	case <-started:
	case <-time.After(5 * time.Second):
		unblock.Do(func() { close(release) })
		t.Fatal("provider probe did not start")
	}
	_, pending := a.Current()
	if pending.Model != "old" || a.Health().State != provider.HealthConfigured {
		t.Fatal("valid provider must be installed while the probe is pending")
	}
	newConfig := agent.ProviderConfig{Provider: "openai", Model: "new", BaseURL: srv.URL + "/v1", APIKey: "test"}
	newProvider, err := agent.NewProviderFromResolved(&newConfig)
	if err != nil {
		unblock.Do(func() { close(release) })
		t.Fatal(err)
	}
	a.Set(newProvider, newConfig)
	unblock.Do(func() { close(release) })
	if err := <-done; err != nil {
		t.Fatalf("probe failure rejected valid configuration: %v", err)
	}
	current, config := a.Current()
	if current != newProvider || config.Model != "new" || a.Health().State != provider.HealthConfigured {
		t.Fatal("late probe overwrote a newer provider or its health")
	}
	if _, _, err := a.Reload(context.Background(), agent.ProviderConfig{Provider: "unsupported"}, nil); err == nil {
		t.Fatal("unsupported provider was accepted")
	}
	current, config = a.Current()
	if current != newProvider || config.Model != "new" || a.Health().State != provider.HealthConfigured {
		t.Fatal("failed construction changed the working provider")
	}
}
