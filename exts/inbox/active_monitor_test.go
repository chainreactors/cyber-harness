package inbox_test

import (
	"context"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	mailbox "github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	harness "github.com/chainreactors/cyber/core/collaboration"
	"github.com/chainreactors/cyber/core/extension"
	loopext "github.com/chainreactors/cyber/exts/agent"
	inboxext "github.com/chainreactors/cyber/exts/inbox"
	ioaclient "github.com/chainreactors/cyber/exts/ioa/client"
	promptext "github.com/chainreactors/cyber/exts/prompt"
	sessionext "github.com/chainreactors/cyber/exts/session"
	subagentext "github.com/chainreactors/cyber/exts/subagent"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/testutil/apptest"
	"github.com/chainreactors/cyber/pkg/testutil/hosttest"
	peerclient "github.com/chainreactors/ioa/client"
	"github.com/chainreactors/ioa/protocols"
	ioaserver "github.com/chainreactors/ioa/server"
)

func TestActiveIOAMonitorRestartAndContinuous(t *testing.T) {
	duration := time.Second
	if raw := os.Getenv("CYBER_ACTIVE_SOAK_DURATION"); raw != "" {
		var err error
		duration, err = time.ParseDuration(raw)
		if err != nil || duration <= 0 {
			t.Fatal("CYBER_ACTIVE_SOAK_DURATION must be a positive duration")
		}
	}
	ctx, cancel := context.WithTimeout(t.Context(), duration+30*time.Second)
	defer cancel()
	store := ioaserver.NewMemoryStore()
	defer store.Close()
	handler := ioaserver.NewHTTPHandler(ioaserver.NewService(store, "active-test"))
	drop := make(chan struct{})
	var feeds atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if strings.HasSuffix(r.URL.Path, "/sse") && feeds.Add(1) == 1 {
			feedCtx, stop := context.WithCancel(r.Context())
			defer stop()
			go func() {
				select {
				case <-drop:
					stop()
				case <-feedCtx.Done():
				}
			}()
			r = r.WithContext(feedCtx)
		}
		handler.ServeHTTP(w, r)
	}))
	defer server.Close()
	endpoint := strings.Replace(server.URL, "http://", "http://active-test@", 1)
	monitor, err := peerclient.NewClient(endpoint, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.EnsureRegistered(ctx, "external-monitor", "", nil); err != nil {
		t.Fatal(err)
	}
	space, err := monitor.Space(ctx, "active-monitor", "monitoring")
	if err != nil {
		t.Fatal(err)
	}
	feedback := make(chan mailbox.Message, 16)
	var executions atomic.Int32
	p := modelFunc(func(_ context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if result, router := activeDecision(r); router {
			return result, nil
		}
		executions.Add(1)
		return response("observed " + lastInput(r)), nil
	})
	dir := t.TempDir()
	var receiverID string
	open := func() (*apptest.Fixture, *inboxext.Router, *extension.Set) {
		f := apptest.NewFixture(t, nil, nil)
		f.Providers.Set(p, provider.ProviderConfig{Model: "scenario", ContextWindow: 200000})
		ioaConfig, err := ioaclient.ConfigFromOption(&cfg.Option{Command: cfg.RunModeInbox, InboxOptions: cfg.InboxOptions{StateDir: dir}, Extensions: cfg.Values{ioaclient.ConfigKey: {"url": endpoint, "space": "active-monitor"}}})
		if err != nil {
			t.Fatal(err)
		}
		connection := ioaclient.New(*ioaConfig)
		router := inboxext.New(inboxext.Config{StateDir: dir, MaxActive: 2, Notify: func(callCtx context.Context, m mailbox.Message) error {
			peer := m.Meta.ReplyNode
			ref := m.Meta.ReplyMessage
			_, err := connection.Service().Client().Send(callCtx, space.ID, protocols.SendMessage{Content: map[string]any{"text": provider.MessageText(m.Message)}, Refs: &protocols.Ref{Nodes: []string{peer}, Messages: []string{ref}}, Meta: harness.Envelope{Version: harness.Version, Event: harness.Message, ID: m.Message.Id}.Meta()})
			if err == nil {
				select {
				case feedback <- m:
				case <-callCtx.Done():
					return callCtx.Err()
				}
			}
			return err
		}})
		adapter := ioaclient.NewCollaboration(ioaclient.CollaborationOptions{Receive: router.Router().Deliver, LoadCursor: router.Router().LoadCursor, SaveCursor: router.Router().SaveCursor})
		entries := append(apptest.Entries(t, f), promptext.New(), loopext.New(agent.StandardLoop{}), subagentext.New(), connection, sessionext.New(session.Config{}), subagentext.NewTools(), adapter, router)
		set := hosttest.Load(t, ctx, entries...)
		if bound, err := connection.Service().ReadySpace(ctx); err != nil || bound != space.ID {
			t.Fatal(err)
		}
		if receiverID == "" {
			receiverID = connection.Service().Client().NodeID()
		} else if receiverID != connection.Service().Client().NodeID() {
			t.Fatal("active IOA address changed across restart")
		}
		return f, router.Router(), set
	}
	_, _, set := open()
	defer func() { closeActive(t, set) }()
	send := func(text, task string) protocols.Message {
		m, err := monitor.Send(ctx, space.ID, protocols.SendMessage{Content: map[string]any{"text": text}, Refs: &protocols.Ref{Nodes: []string{receiverID}}, Meta: harness.Envelope{Version: harness.Version, Event: harness.Message, Source: harness.Address{SessionID: "external-feed"}, Target: harness.Address{TaskID: task}}.Meta()})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	first := send("initial observation", "")
	m := waitFor(t, ctx, feedback)
	task := m.Meta.TaskID
	if task == "" || m.Meta.ReplyMessage != first.ID {
		t.Fatalf("source correlation=%v", m.Meta)
	}
	// Cut the actual SSE connection. Source history must cover the gap.
	close(drop)
	send("observation during disconnect", task)
	waitFor(t, ctx, feedback)
	closeActive(t, set)
	// An event produced while the entire host is down is replayed on startup.
	offline := send("observation while host offline", task)
	f, router, reopened := open()
	set = reopened
	m = waitFor(t, ctx, feedback)
	if m.Meta.TaskID != task || m.Meta.ReplyMessage != first.ID {
		t.Fatalf("restored task/source=%v", m.Meta)
	}
	// Re-submit the source envelope already admitted via replay; no new run.
	duplicate := mailbox.NewMessage(mailbox.OriginPeer, "user", "observation while host offline")
	duplicate.Message.Id = offline.ID
	duplicate.Meta = mailbox.Metadata{Sender: monitor.NodeID(), MessageID: offline.ID, SourceSessionID: "external-feed", TaskID: task}
	activeSubmit(t, ctx, router, duplicate)
	started := time.Now()
	n := 0
	for time.Since(started) < duration {
		select {
		case <-ctx.Done():
			t.Fatal(ctx.Err())
		case <-time.After(100 * time.Millisecond):
		}
		send(fmt.Sprintf("continuous observation %d", n), task)
		if m := waitFor(t, ctx, feedback); m.Meta.TaskID != task {
			t.Fatal("monitor spawned a different task")
		}
		n++
	}
	if got := int(executions.Load()); got != n+3 {
		t.Fatalf("missing/duplicate executions: got=%d want=%d", got, n+3)
	}
	for _, info := range f.Shell.Manager().List() {
		if info.Kind == "agent" && info.Name != "router" && info.State.Active() {
			if _, err := f.Shell.Manager().Wait(ctx, info.ID, 0); err != nil {
				t.Fatal(err)
			}
		}
	}
	if feeds.Load() < 3 {
		t.Fatalf("SSE reconnect/restart not exercised: %d", feeds.Load())
	}
	t.Logf("active monitoring ran %s, %d source events, %d SSE connections; duplicate replay did not reexecute", time.Since(started).Round(time.Millisecond), n+3, feeds.Load())
}
