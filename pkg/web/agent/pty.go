package agent

import (
	"context"
	"sync"
	"time"

	"github.com/chainreactors/aiscan/agent/tmux"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/utils/pty"
)

// NewPTYRouter creates the tool-node fallback router. Agent transports receive
// their router directly from AgentRuntime and do not inspect the bash tool.
func NewPTYRouter(reg *commands.CommandRegistry) *pty.Router {
	mgr := RegistryPTYManager(reg)
	var baseMgr *pty.Manager
	if mgr != nil {
		baseMgr = mgr.Manager
	}
	openers := pty.DefaultOpeners(baseMgr, pty.DefaultSessionTimeout, pty.DefaultEnv())
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
func SubscribePTYSessions(ctx context.Context, mgr *tmux.Manager, router *pty.Router, send func(pty.Frame)) func() {
	if mgr == nil || router == nil || send == nil {
		return func() {}
	}
	notify := make(chan tmux.EventAction, 1)
	unsub := mgr.Subscribe(func(ev tmux.Event) {
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
				BroadcastPTYSessions(mgr, router, send)
			case <-ticker.C:
				if dirty {
					dirty = false
					BroadcastPTYSessions(mgr, router, send)
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
func BroadcastPTYSessions(mgr *tmux.Manager, router *pty.Router, send func(pty.Frame)) {
	streamIDs := router.StreamIDs()
	if len(streamIDs) == 0 {
		return
	}
	sessions := mgr.List()
	for _, streamID := range streamIDs {
		send(pty.Frame{Type: pty.FrameSessions, StreamID: streamID, Sessions: sessions})
	}
}
