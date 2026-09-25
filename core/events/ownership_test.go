package events_test

import (
	"sync"
	"testing"

	aop "github.com/chainreactors/cyber/aop"
	coreevents "github.com/chainreactors/cyber/core/events"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestPublishConcurrentProducersAndReentrantObserver(t *testing.T) {
	stream := coreevents.New()
	a := stream
	var mu sync.Mutex
	seen := make(map[uint64]*aop.Event)
	a.Observe(func(event *aop.Event) {
		mu.Lock()
		if seen[event.Seq] != nil {
			t.Errorf("duplicate sequence %d", event.Seq)
		}
		seen[event.Seq] = event
		mu.Unlock()
		if event.Id == "outer" {
			a.Publish(&aop.Event{SessionId: "shared", Id: "nested"})
		}
	})
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
