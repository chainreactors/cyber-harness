package events

import (
	"context"
	"sync"
	"testing"

	aop "github.com/chainreactors/cyber/aop"
	"google.golang.org/protobuf/types/known/timestamppb"
)

func TestStreamIsTheSingleConcurrentStampingAuthority(t *testing.T) {
	stream := New()
	var mu sync.Mutex
	seen := make(map[uint64]*aop.Event)
	stream.Observe(ObserverFunc(func(event *aop.Event) {
		mu.Lock()
		if seen[event.Seq] != nil {
			t.Errorf("duplicate sequence %d", event.Seq)
		}
		seen[event.Seq] = event
		mu.Unlock()
		if event.Id == "outer" {
			stream.Publish(&aop.Event{SessionId: "shared", Id: "nested"})
		}
	}))

	stamp := timestamppb.Now()
	outer := &aop.Event{SessionId: "shared", Id: "outer", EmittedAt: stamp}
	stream.Publish(outer)
	var producers sync.WaitGroup
	for range 32 {
		producers.Go(func() { stream.Publish(&aop.Event{SessionId: "shared"}) })
	}
	producers.Wait()

	mu.Lock()
	defer mu.Unlock()
	if seen[1] != outer || outer.EmittedAt != stamp || outer.Id != "outer" {
		t.Fatal("stream replaced caller-owned event metadata")
	}
	for seq := uint64(1); seq <= 34; seq++ {
		if event := seen[seq]; event == nil || event.EmittedAt == nil || event.Id == "" {
			t.Fatalf("missing event or metadata at sequence %d", seq)
		}
	}
}

func TestObserverPanicDoesNotEscapePublication(t *testing.T) {
	stream := New()
	failed := stream.Observe(ObserverFunc(func(*aop.Event) { panic("broken observer") }))
	defer failed.Cancel()
	var observed bool
	healthy := stream.Observe(ObserverFunc(func(*aop.Event) { observed = true }))
	defer healthy.Cancel()
	stream.Publish(&aop.Event{})
	if !observed {
		t.Fatal("observer panic stopped later observations")
	}
	if err := failed.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
}

func TestStreamSequencesSessionlessRootEvents(t *testing.T) {
	stream := New()
	var got []*aop.Event
	stream.Observe(ObserverFunc(func(event *aop.Event) { got = append(got, event) }))
	stream.Publish(&aop.Event{})
	stream.Publish(&aop.Event{})
	if len(got) != 2 || got[0].Seq != 1 || got[1].Seq != 2 {
		t.Fatalf("root sequence = %v", got)
	}
}
