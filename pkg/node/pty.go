package node

import (
	"context"
	"sync"
	"time"

	"github.com/chainreactors/cyber/agent/tmux"
	ptypb "github.com/chainreactors/cyber/aop/pty"
	ptyext "github.com/chainreactors/cyber/pkg/exts/pty"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
	"github.com/chainreactors/utils/pty"
)

// NewPTYRouter creates the tool-node fallback router. Agent transports receive
// their router directly from Manager and do not inspect the bash tool.
func NewPTYRouter(bash *terminaltool.BashTool) *ptyext.Router {
	mgr := RegistryPTYManager(bash)
	if mgr == nil {
		return ptyext.NewRuntimeRouter(nil)
	}
	return ptyext.NewRuntimeRouter(mgr.Manager)
}

// RegistryPTYManager extracts the tmux Manager from the "bash" tool in the
// command registry, if available.
func RegistryPTYManager(bash *terminaltool.BashTool) *tmux.Manager {
	if bash == nil {
		return nil
	}
	return bash.Manager()
}

// SubscribePTYSessions subscribes to PTY session changes and broadcasts
// session state to all active PTY streams.
func SubscribePTYSessions(ctx context.Context, mgr *tmux.Manager, router *ptyext.Router, send func(*ptypb.ProtocolMessage)) func() {
	if mgr == nil || router == nil || send == nil {
		return func() {}
	}
	notify := make(chan pty.EventAction, 1)
	unsub := mgr.Subscribe(func(ev pty.Event) {
		switch ev.Action {
		case pty.EventSessionCreated, pty.EventSessionUpdated, pty.EventSessionOutput, pty.EventSessionClosed:
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
				if action == pty.EventSessionOutput {
					dirty = true
					continue
				}
				dirty = false
				BroadcastPTYSessions(router, send)
			case <-ticker.C:
				if dirty {
					dirty = false
					BroadcastPTYSessions(router, send)
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
			unsub.Cancel()
			close(stop)
		})
	}
}

// BroadcastPTYSessions sends the current PTY session list to all active streams.
func BroadcastPTYSessions(router *ptyext.Router, send func(*ptypb.ProtocolMessage)) {
	router.BroadcastSessions(send)
}
