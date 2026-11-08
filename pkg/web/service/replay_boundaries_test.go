package service

import (
	"fmt"
	"strconv"
	"testing"

	aop "github.com/chainreactors/cyber/aop"
)

func TestReplayFromZeroAndPaginationPreserveEarlyEvents(t *testing.T) {
	store, err := NewSQLiteStore(t.TempDir()+"/chat.db", ScanSchema)
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	createStoredSession(t, store, "long-history")
	const count = 503
	for i := 1; i <= count; i++ {
		event := &aop.Event{Id: fmt.Sprintf("event-%d", i), SessionId: "long-history", Seq: uint64(i), Emitter: "node", Payload: &aop.Event_Message{Message: &aop.Message{Id: fmt.Sprintf("message-%d", i), Role: "user", Content: []*aop.Content{aop.Text(fmt.Sprintf("context-%d", i))}}}}
		if _, _, err := store.AppendAOPEvent(t.Context(), "long-history", event); err != nil {
			t.Fatal(err)
		}
	}
	for _, limit := range []int{1, 500} {
		t.Run(fmt.Sprintf("page=%d", limit), func(t *testing.T) {
			var after int64
			seen := 0
			for {
				page, next, err := store.ListAOPEventsAfter(t.Context(), "long-history", after, limit)
				if err != nil {
					t.Fatal(err)
				}
				for _, item := range page {
					seen++
					if item.Event.Id != fmt.Sprintf("event-%d", seen) {
						t.Fatalf("missing early context at offset %d: %s", seen, item.Event.Id)
					}
					cursor, err := strconv.ParseInt(item.Cursor, 10, 64)
					if err != nil || cursor <= after {
						t.Fatalf("cursor did not advance: %s after %d", item.Cursor, after)
					}
					after = cursor
				}
				if next == 0 {
					break
				}
				after = next
			}
			if seen != count {
				t.Fatalf("history lost events: replayed %d of %d", seen, count)
			}
		})
	}
	all, _, err := store.ListAOPEventsAfter(t.Context(), "long-history", 0, 0)
	if err != nil || len(all) != count || all[0].Event.Id != "event-1" {
		t.Fatalf("complete replay = %d events, %v", len(all), err)
	}
	// The explicit tail API still serves the most recent page for callers that
	// ask for it. It does not define the semantics of forward cursor replay.
	tail, _, err := store.ListAOPEventPage(t.Context(), "long-history", 0, 1)
	if err != nil || len(tail) != 1 || tail[0].Event.Id != fmt.Sprintf("event-%d", count) {
		t.Fatalf("tail page = %v, %v", tail, err)
	}
}
