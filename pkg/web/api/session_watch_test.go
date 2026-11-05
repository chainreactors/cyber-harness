package api

import (
	"context"
	"fmt"
	"strconv"
	"testing"

	aop "github.com/chainreactors/cyber/aop"
	types "github.com/chainreactors/cyber/core/types"
	"google.golang.org/protobuf/types/known/timestamppb"
)

type pagedMockStore struct {
	sessionTestStore
	events     []*aop.EventDelivery
	limitCalls []int
}

func (s *pagedMockStore) ListAOPEventsAfter(_ context.Context, _ string, after int64, limit int) ([]*aop.EventDelivery, error) {
	s.limitCalls = append(s.limitCalls, limit)
	if limit <= 0 {
		return nil, fmt.Errorf("unbounded query rejected: limit must be > 0, got %d", limit)
	}
	var res []*aop.EventDelivery
	for _, item := range s.events {
		cur, _ := strconv.ParseInt(item.Cursor, 10, 64)
		if cur > after {
			res = append(res, item)
			if len(res) >= limit {
				break
			}
		}
	}
	return res, nil
}

func TestWatchEventsReplayIsBoundedAndPaged(t *testing.T) {
	const totalEvents = 600
	events := make([]*aop.EventDelivery, totalEvents)
	for i := 0; i < totalEvents; i++ {
		cursorStr := strconv.Itoa(i + 1)
		events[i] = &aop.EventDelivery{
			Cursor: cursorStr,
			Event: &aop.Event{
				Id:        cursorStr,
				SessionId: "session-1",
				EmittedAt: timestamppb.Now(),
				Payload:   &aop.Event_Status{Status: &aop.Status{State: "running"}},
			},
		}
	}

	store := &pagedMockStore{
		events: events,
		sessionTestStore: sessionTestStore{
			session: &types.SessionRecord{
				Session: &aop.Session{Id: "session-1", State: SessionStateOpen},
			},
		},
	}
	runtime := &sessionTestRuntime{connected: true}
	sessions := NewSessions(store, runtime, func() string { return "gen" })

	ctx, cancel := context.WithCancel(context.Background())
	var received []*aop.EventDelivery
	err := sessions.WatchEvents(ctx, &aop.WatchEventsRequest{
		SessionId: "session-1",
	}, func(d *aop.EventDelivery) error {
		received = append(received, d)
		if len(received) == totalEvents {
			cancel()
		}
		return nil
	})
	if err != nil && err != context.Canceled {
		t.Fatalf("WatchEvents error = %v", err)
	}

	if len(store.limitCalls) == 0 {
		t.Fatal("ListAOPEventsAfter was not called")
	}
	for i, l := range store.limitCalls {
		if l <= 0 {
			t.Fatalf("call %d had unbounded limit: %d", i, l)
		}
		if l > 256 {
			t.Fatalf("call %d limit %d exceeded max page size 256", i, l)
		}
	}
	if len(store.limitCalls) < 3 {
		t.Fatalf("expected at least 3 paged calls for %d events, got %d calls: %v", totalEvents, len(store.limitCalls), store.limitCalls)
	}
	if len(received) != totalEvents {
		t.Fatalf("received %d events, want %d", len(received), totalEvents)
	}
	for i, d := range received {
		expectedCursor := strconv.Itoa(i + 1)
		if d.Cursor != expectedCursor {
			t.Fatalf("event %d has cursor %q, want %q", i, d.Cursor, expectedCursor)
		}
	}
}

func BenchmarkWatchEventsReplay(b *testing.B) {
	const totalEvents = 1000
	events := make([]*aop.EventDelivery, totalEvents)
	for i := 0; i < totalEvents; i++ {
		cursorStr := strconv.Itoa(i + 1)
		events[i] = &aop.EventDelivery{
			Cursor: cursorStr,
			Event: &aop.Event{
				Id:        cursorStr,
				SessionId: "session-1",
				EmittedAt: timestamppb.Now(),
				Payload:   &aop.Event_Status{Status: &aop.Status{State: "running"}},
			},
		}
	}

	store := &pagedMockStore{
		events: events,
		sessionTestStore: sessionTestStore{
			session: &types.SessionRecord{
				Session: &aop.Session{Id: "session-1", State: SessionStateOpen},
			},
		},
	}
	runtime := &sessionTestRuntime{connected: true}
	sessions := NewSessions(store, runtime, func() string { return "gen" })

	b.ResetTimer()
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		ctx, cancel := context.WithCancel(context.Background())
		receivedCount := 0
		_ = sessions.WatchEvents(ctx, &aop.WatchEventsRequest{
			SessionId: "session-1",
		}, func(d *aop.EventDelivery) error {
			receivedCount++
			if receivedCount >= totalEvents {
				cancel()
			}
			return nil
		})
	}
}
