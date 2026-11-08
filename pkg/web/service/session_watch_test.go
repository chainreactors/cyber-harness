package service

import (
	"context"
	"errors"
	"slices"
	"strconv"
	"strings"
	"testing"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	types "github.com/chainreactors/cyber/core/types"
	"google.golang.org/protobuf/proto"
)

func TestWatchEventsReplayPagesAndFilters(t *testing.T) {
	for _, scenario := range []struct {
		name                     string
		filteredFrom, filteredTo int
		after                    int
	}{
		{name: "complete history"},
		{name: "short filtered page", filteredFrom: 100, filteredTo: 100},
		{name: "empty first page", filteredFrom: 1, filteredTo: 256},
		{name: "empty middle page", filteredFrom: 257, filteredTo: 512},
		{name: "empty trailing pages", filteredFrom: 257, filteredTo: 600},
		{name: "resume across empty page", filteredFrom: 129, filteredTo: 384, after: 128},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			store, err := NewSQLiteStore(t.TempDir() + "/watch.db")
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			createStoredSession(t, store, "replay")
			var want []string
			for i := 1; i <= 600; i++ {
				event := &aop.Event{Id: strconv.Itoa(i), SessionId: "replay",
					Payload: &aop.Event_Status{Status: &aop.Status{State: "running"}}}
				if i >= scenario.filteredFrom && i <= scenario.filteredTo {
					if i%2 == 0 {
						event.SessionId = ""
					} else {
						event.Payload = nil
					}
				} else if i > scenario.after {
					want = append(want, event.Id)
				}
				if err := store.AddAOPEvent(t.Context(), "replay", event); err != nil {
					t.Fatal(err)
				}
			}
			want = append(want, "appended", "live")
			svc := NewService(ServiceConfig{Store: store})
			defer svc.Close(context.Background())
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			var got []string
			err = svc.api.Sessions.WatchEvents(ctx, &aop.WatchEventsRequest{
				SessionId: "replay", AfterCursor: strconv.Itoa(scenario.after),
			}, func(delivery *aop.EventDelivery) error {
				got = append(got, delivery.Event.Id)
				if len(got) == 1 {
					// Write during replay to check connection release and replay/live deduplication.
					svc.BroadcastAOPEvent("replay", &aop.Event{Id: "appended", SessionId: "replay",
						Payload: &aop.Event_Status{Status: &aop.Status{State: "running"}}})
					svc.BroadcastAOPEvent("replay", &aop.Event{Id: "live", SessionId: "replay",
						Payload: &aop.Event_MessageDelta{MessageDelta: &aop.MessageDelta{}}})
				}
				if delivery.Event.Id == "live" {
					cancel()
				}
				return nil
			})
			if !errors.Is(err, context.Canceled) || !slices.Equal(got, want) {
				t.Fatalf("replay returned %v, received %d events, want %d; order matches: %v", err, len(got), len(want), slices.Equal(got, want))
			}
		})
	}
}

func TestWatchEventsSendsBeforeReadingLaterPages(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir() + "/watch.db")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createStoredSession(t, store, "replay")
	for i := 1; i <= 257; i++ {
		if err := store.AddAOPEvent(t.Context(), "replay", &aop.Event{Id: strconv.Itoa(i), SessionId: "replay",
			Payload: &aop.Event_Status{Status: &aop.Status{State: "running"}}}); err != nil {
			t.Fatal(err)
		}
	}
	// A later decode error must not prevent the first page from being delivered.
	if _, err := store.db.Exec(`UPDATE chat_aop_events SET event_proto = ? WHERE session_id = ? AND cursor = ?`, []byte{0xff}, "replay", 257); err != nil {
		t.Fatal(err)
	}
	svc := NewService(ServiceConfig{Store: store})
	defer svc.Close(context.Background())
	sendErr := errors.New("send failed")
	for _, scenario := range []struct {
		name string
		want int
		err  error
	}{
		{name: "later decode error", want: 256},
		{name: "cancel between pages", want: 256, err: context.Canceled},
		{name: "send error", want: 1, err: sendErr},
	} {
		t.Run(scenario.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(t.Context(), 5*time.Second)
			defer cancel()
			received := 0
			err := svc.api.Sessions.WatchEvents(ctx, &aop.WatchEventsRequest{SessionId: "replay"}, func(*aop.EventDelivery) error {
				received++
				if scenario.err == context.Canceled && received == scenario.want {
					cancel()
				}
				if scenario.err == sendErr {
					return sendErr
				}
				return nil
			})
			if received != scenario.want {
				t.Fatalf("received %d events, want %d", received, scenario.want)
			}
			if scenario.err != nil {
				if !errors.Is(err, scenario.err) {
					t.Fatalf("WatchEvents error = %v, want %v", err, scenario.err)
				}
			} else if err == nil || !strings.Contains(err.Error(), "decode AOP event at cursor 257") {
				t.Fatalf("WatchEvents error = %v, want decode error at 257", err)
			}
		})
	}
}

func BenchmarkWatchEventsReplay(b *testing.B) {
	for _, total := range []int{1000, 10000, 50000} {
		b.Run(strconv.Itoa(total), func(b *testing.B) {
			store, err := NewSQLiteStore(b.TempDir() + "/watch.db")
			if err != nil {
				b.Fatal(err)
			}
			defer store.Close()
			if err := store.CreateSession(b.Context(), &types.SessionRecord{Session: &aop.Session{Id: "replay"}}); err != nil {
				b.Fatal(err)
			}
			for start := 1; start <= total; start += 1000 {
				models := make([]aopEventModel, 0, 1000)
				for i := start; i < start+1000 && i <= total; i++ {
					id := strconv.Itoa(i)
					raw, err := proto.Marshal(&aop.Event{Id: id, SessionId: "replay",
						Payload: &aop.Event_Message{Message: &aop.Message{Role: "assistant", Content: []*aop.Content{aop.Text(strings.Repeat("x", 256))}}}})
					if err != nil {
						b.Fatal(err)
					}
					models = append(models, aopEventModel{ID: id, SessionID: "replay", EventID: id, Cursor: int64(i), EventProto: raw})
				}
				if _, err := store.orm.NewInsert().Model(&models).Exec(b.Context()); err != nil {
					b.Fatal(err)
				}
			}
			svc := NewService(ServiceConfig{Store: store})
			defer svc.Close(context.Background())
			var firstTotal time.Duration
			b.ReportAllocs()
			b.ResetTimer()
			for i := 0; i < b.N; i++ {
				ctx, cancel := context.WithCancel(b.Context())
				received := 0
				started := time.Now()
				err := svc.api.Sessions.WatchEvents(ctx, &aop.WatchEventsRequest{SessionId: "replay"}, func(*aop.EventDelivery) error {
					if received == 0 {
						firstTotal += time.Since(started)
					}
					received++
					if received == total {
						cancel()
					}
					return nil
				})
				cancel()
				if !errors.Is(err, context.Canceled) || received != total {
					b.Fatalf("replay received %d of %d events: %v", received, total, err)
				}
			}
			b.ReportMetric(float64(firstTotal.Nanoseconds())/float64(b.N), "first-ns/op")
		})
	}
}
