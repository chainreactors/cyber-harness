package node

import (
	"context"
	"encoding/json"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/webproto"
	"github.com/chainreactors/utils/pty"
)

// NewPTYRouter creates a PTY router with default openers plus any caller-supplied
// extra openers (e.g. a REPL opener from the agent runtime). The caller does not
// need to import core/runner; instead it passes the extra openers map.
func NewPTYRouter(reg *commands.CommandRegistry, extraOpeners map[string]pty.OpenFunc) *pty.Router {
	mgr := RegistryPTYManager(reg)
	var baseMgr *pty.Manager
	if mgr != nil {
		baseMgr = mgr.Manager
	}
	openers := pty.DefaultOpeners(baseMgr, pty.DefaultSessionTimeout, pty.DefaultEnv())
	for k, v := range extraOpeners {
		openers[k] = v
	}
	return pty.NewRouter(baseMgr, pty.WithOpeners(openers))
}

// RegistryPTYManager extracts the tmux Manager from the "bash" tool in the
// command registry, if available.
func RegistryPTYManager(reg *commands.CommandRegistry) *tmux.Manager {
	if reg == nil {
		return nil
	}
	tool, ok := reg.GetTool("bash")
	if !ok {
		return nil
	}
	manager, ok := tool.(interface {
		Manager() *tmux.Manager
	})
	if !ok {
		return nil
	}
	return manager.Manager()
}

// SubscribePTYSessions subscribes to PTY session changes and broadcasts
// session state to all active PTY streams.
func SubscribePTYSessions(ctx context.Context, mgr *tmux.Manager, router *pty.Router, send func(webproto.Message)) func() {
	if mgr == nil || router == nil || send == nil {
		return func() {}
	}
	activity := NewPTYActivityTracker()
	notify := make(chan tmux.EventAction, 1)
	unsub := mgr.Subscribe(func(ev tmux.Event) {
		activity.Observe(ev)
		switch ev.Action {
		case tmux.EventSessionCreated, tmux.EventSessionUpdated, tmux.EventSessionOutput, tmux.EventSessionClosed:
			select {
			case notify <- ev.Action:
			default:
			}
		}
	})
	stop := make(chan struct{})
	go func() {
		ticker := time.NewTicker(350 * time.Millisecond)
		defer ticker.Stop()
		dirty := false
		for {
			select {
			case action := <-notify:
				if action == tmux.EventSessionOutput {
					dirty = true
					continue
				}
				dirty = false
				BroadcastPTYSessions(mgr, router, activity, send)
			case <-ticker.C:
				if dirty {
					dirty = false
					BroadcastPTYSessions(mgr, router, activity, send)
				}
			case <-ctx.Done():
				return
			case <-stop:
				return
			}
		}
	}()
	var once sync.Once
	return func() {
		once.Do(func() {
			unsub()
			close(stop)
		})
	}
}

// BroadcastPTYSessions sends the current PTY session list to all active streams.
func BroadcastPTYSessions(mgr *tmux.Manager, router *pty.Router, activity *PTYActivityTracker, send func(webproto.Message)) {
	streamIDs := router.StreamIDs()
	if len(streamIDs) == 0 {
		return
	}
	sessions := PTYSessionViews(mgr.List(), activity)
	for _, streamID := range streamIDs {
		payload, _ := json.Marshal(map[string]any{"sessions": sessions})
		send(webproto.Message{Type: "pty.sessions", StreamID: streamID, Payload: payload})
	}
}

// PTYActivity tracks per-session activity metrics.
type PTYActivity struct {
	LastActivityAt time.Time `json:"last_activity_at,omitempty"`
	ActivitySeq    int64     `json:"activity_seq,omitempty"`
	OutputBytes    int64     `json:"output_bytes,omitempty"`
}

// PTYActivityTracker tracks activity across all PTY sessions.
type PTYActivityTracker struct {
	mu       sync.Mutex
	sessions map[string]PTYActivity
}

// PTYSessionView combines session info with activity metrics.
type PTYSessionView struct {
	tmux.Info
	LastActivityAt time.Time `json:"last_activity_at,omitempty"`
	ActivitySeq    int64     `json:"activity_seq,omitempty"`
	OutputBytes    int64     `json:"output_bytes,omitempty"`
}

// NewPTYActivityTracker creates a new activity tracker.
func NewPTYActivityTracker() *PTYActivityTracker {
	return &PTYActivityTracker{sessions: make(map[string]PTYActivity)}
}

// Observe records a PTY event.
func (t *PTYActivityTracker) Observe(ev tmux.Event) {
	if t == nil || ev.Info.ID == "" {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	activity := t.sessions[ev.Info.ID]
	now := time.Now()
	if activity.LastActivityAt.IsZero() {
		activity.LastActivityAt = ev.Info.StartedAt
		if activity.LastActivityAt.IsZero() {
			activity.LastActivityAt = now
		}
	}
	switch ev.Action {
	case tmux.EventSessionOutput:
		activity.LastActivityAt = now
		activity.ActivitySeq++
		activity.OutputBytes += int64(ev.OutputBytes)
	case tmux.EventSessionCreated, tmux.EventSessionUpdated, tmux.EventSessionClosed:
		activity.LastActivityAt = now
		activity.ActivitySeq++
	}
	t.sessions[ev.Info.ID] = activity
}

// Snapshot returns the activity data for a given session ID.
func (t *PTYActivityTracker) Snapshot(id string) PTYActivity {
	if t == nil || id == "" {
		return PTYActivity{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessions[id]
}

// PTYSessionViews combines session info with activity snapshots.
func PTYSessionViews(sessions []tmux.Info, activity *PTYActivityTracker) []PTYSessionView {
	views := make([]PTYSessionView, 0, len(sessions))
	for _, session := range sessions {
		snapshot := activity.Snapshot(session.ID)
		if snapshot.LastActivityAt.IsZero() {
			snapshot.LastActivityAt = session.EndedAt
		}
		if snapshot.LastActivityAt.IsZero() {
			snapshot.LastActivityAt = session.StartedAt
		}
		views = append(views, PTYSessionView{
			Info:           session,
			LastActivityAt: snapshot.LastActivityAt,
			ActivitySeq:    snapshot.ActivitySeq,
			OutputBytes:    snapshot.OutputBytes,
		})
	}
	return views
}
