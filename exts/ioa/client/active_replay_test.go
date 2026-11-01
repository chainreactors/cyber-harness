package client

import (
	"context"
	"errors"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/telemetry"
	promptext "github.com/chainreactors/cyber/exts/prompt"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/testutil/hosttest"
	service "github.com/chainreactors/cyber/tools/ioa"
	"github.com/chainreactors/ioa/protocols"
	ioaserver "github.com/chainreactors/ioa/server"
)

func TestActiveReplayFullHistoryCursorFailureAndStaleCursor(t *testing.T) {
	ctx := t.Context()
	store := ioaserver.NewMemoryStore()
	defer store.Close()
	svc := ioaserver.NewService(store, "test")
	producer := ioaserver.NewLocalClient(svc, "")
	if _, err := producer.RegisterNode(ctx, "source", "", nil); err != nil {
		t.Fatal(err)
	}
	space, err := producer.Space(ctx, "history", "history")
	if err != nil {
		t.Fatal(err)
	}
	for i := 1; i <= 300; i++ {
		if err := store.AppendMessage(protocols.Message{ID: fmt.Sprintf("%032x", i), SpaceID: space.ID, Sender: producer.NodeID(), Content: map[string]any{"text": fmt.Sprintf("event-%d", i)}, Meta: map[string]any{"target_session_id": "old-execution", "task_id": "durable-task"}}); err != nil {
			t.Fatal(err)
		}
	}
	server := httptest.NewServer(ioaserver.NewHTTPHandler(svc))
	defer server.Close()
	connection := New(service.Config{URL: strings.Replace(server.URL, "http://", "http://test@", 1), AutoRegister: true})
	cursor := ""
	accepted := map[string]int{}
	failedSave, failedAdmission := false, false
	adapter := NewCollaboration(CollaborationOptions{
		LoadCursor: func(string) (string, error) { return cursor, nil },
		SaveCursor: func(_ string, id string) error {
			if id == fmt.Sprintf("%032x", 100) && !failedSave {
				failedSave = true
				return errors.New("cursor storage unavailable")
			}
			cursor = id
			return nil
		},
		Receive: func(_ context.Context, m inbox.Message) error {
			if m.Message.Id == fmt.Sprintf("%032x", 200) && !failedAdmission {
				failedAdmission = true
				return inbox.ErrInboxFull
			}
			if m.Meta["task_id"] != "durable-task" || m.Meta["target_session_id"] != "old-execution" {
				return errors.New("source correlation lost")
			}
			accepted[m.Message.Id]++
			return nil
		},
	})
	set := hosttest.Load(t, ctx, extension.Provided[*hooks.Registry](hooks.New()), extension.Provided[telemetry.Logger](telemetry.NopLogger()), promptext.New(), connection, adapter)
	defer set.Close(context.Background())
	if err := adapter.replay(ctx, space.ID); err == nil || cursor != fmt.Sprintf("%032x", 99) {
		t.Fatalf("failed cursor advanced: cursor=%s error=%v", cursor, err)
	}
	if err := adapter.replay(ctx, space.ID); !errors.Is(err, inbox.ErrInboxFull) || cursor != fmt.Sprintf("%032x", 199) {
		t.Fatalf("unaccepted event advanced cursor: cursor=%s error=%v", cursor, err)
	}
	if err := adapter.replay(ctx, space.ID); err != nil {
		t.Fatal(err)
	}
	if len(accepted) != 300 || cursor != fmt.Sprintf("%032x", 300) {
		t.Fatalf("initial history was truncated: count=%d cursor=%s", len(accepted), cursor)
	}
	cursor = "deleted-cursor"
	if err := adapter.replay(ctx, space.ID); err != nil {
		t.Fatal(err)
	}
	for id, count := range accepted {
		if count != 1 {
			t.Fatalf("duplicate delivery %s count=%d", id, count)
		}
	}
	if cursor != fmt.Sprintf("%032x", 300) {
		t.Fatalf("stale cursor did not recover: %s", cursor)
	}
	empty, err := producer.Space(ctx, "empty-history", "empty-history")
	if err != nil {
		t.Fatal(err)
	}
	cursor = "deleted-cursor"
	if err := adapter.replay(ctx, empty.ID); err != nil || cursor != "" {
		t.Fatalf("empty source retained stale cursor: cursor=%s error=%v", cursor, err)
	}
}

func TestActiveIdentityFollowsStateDirectory(t *testing.T) {
	o := &cfg.Option{Command: cfg.RunModeInbox, InboxOptions: cfg.InboxOptions{StateDir: t.TempDir()}}
	first, err := ConfigFromOption(o)
	if err != nil {
		t.Fatal(err)
	}
	second, err := ConfigFromOption(o)
	if err != nil {
		t.Fatal(err)
	}
	if a, b := first.Identity.IOABinding(), second.Identity.IOABinding(); a.Namespace != b.Namespace || a.Subject != b.Subject {
		t.Fatal("active IOA identity changes on restart")
	}
	o.StateDir = t.TempDir()
	third, err := ConfigFromOption(o)
	if err != nil {
		t.Fatal(err)
	}
	if first.Identity.IOABinding().Subject == third.Identity.IOABinding().Subject {
		t.Fatal("unrelated active hosts share IOA identity")
	}
}
