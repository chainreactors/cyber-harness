package web

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"

	types "github.com/chainreactors/aiscan/pkg/types"
)

// localProc is the process handle for one launched `aiscan agent` child.
type localProc struct {
	name string // --ioa-node-name, also the stable handle used to stop it
	pid  int
	cmd  *exec.Cmd
}

// LocalAgents launches and tracks `aiscan agent` subprocesses on the hub host so
// they register in the pool and can be listed/stopped from the UI. Each child
// dials the hub's own loopback web + IOA endpoints, so it shows up in the pool
// like any node. The hub holds the only handle to these processes, so StopAll
// kills them on shutdown rather than leaving orphans.
type LocalAgents struct {
	serverURL  string     // authenticated hub base; Agent derives /api/aop/ws and /ioa
	configFile string     // explicit hub config inherited by hub-launched children
	pool       *AgentPool // live pool, for registration/busy cross-reference

	mu    sync.Mutex
	procs []*localProc
	seq   int
}

// NewLocalAgents builds a launcher. hubURL is the loopback AIScan server base
// the children dial (e.g. http://127.0.0.1:8080); accessKey is embedded once in
// that URL and the Agent derives both its AOP WebSocket and /ioa endpoints.
func NewLocalAgents(hubURL, accessKey, configFile string, pool *AgentPool) *LocalAgents {
	return &LocalAgents{
		serverURL:  webURLWithToken(hubURL, accessKey),
		configFile: strings.TrimSpace(configFile),
		pool:       pool,
	}
}

// webURLWithToken embeds the access token as userinfo on the hub's loopback web
// URL (http://<token>@host), so a launched agent can authenticate its
// /api/aop/ws pool connection — the hub gates /api/* behind that key. An empty
// token or unparseable hubURL yields hubURL unchanged.
func webURLWithToken(hubURL, token string) string {
	if hubURL == "" || token == "" {
		return hubURL
	}
	u, err := url.Parse(strings.TrimRight(hubURL, "/"))
	if err != nil {
		return hubURL
	}
	u.User = url.User(token)
	return u.String()
}

// Launch spawns an `aiscan agent` on the hub host wired to the hub's loopback
// web + IOA endpoints, and tracks it. The LLM provider/model/key arrive via the
// hub's config push on registration, so nothing about the model is passed here.
func (l *LocalAgents) Launch(ctx context.Context) (*types.LocalAgent, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.serverURL == "" {
		return nil, fmt.Errorf("hub local address unknown; cannot launch a local agent (check the web --addr)")
	}
	bin, err := os.Executable()
	if err != nil {
		return nil, fmt.Errorf("resolve agent binary: %w", err)
	}

	l.mu.Lock()
	l.seq++
	name := fmt.Sprintf("local-%d", l.seq)
	l.mu.Unlock()

	args := []string{
		"agent",
		"--server-url", l.serverURL,
		"--space", "default",
		"--node-name", name,
	}
	if l.configFile != "" {
		args = append([]string{"--config", l.configFile}, args...)
	}
	cmd := exec.Command(bin, args...)
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start local agent: %w", err)
	}
	p := &localProc{name: name, pid: cmd.Process.Pid, cmd: cmd}

	l.mu.Lock()
	l.procs = append(l.procs, p)
	l.mu.Unlock()

	// Drop the entry once the child exits (on its own or via Stop) so the list
	// never shows a dead node.
	go func() {
		_ = cmd.Wait()
		l.remove(p)
	}()

	v := l.view(p)
	return v, nil
}

// List returns the tracked local agents (launch order), cross-referenced with
// the pool for connection state.
func (l *LocalAgents) List() []*types.LocalAgent {
	l.mu.Lock()
	all := make([]*localProc, len(l.procs))
	copy(all, l.procs)
	l.mu.Unlock()

	views := make([]*types.LocalAgent, 0, len(all))
	for _, p := range all {
		views = append(views, l.view(p))
	}
	return views
}

// Stop kills a tracked local agent by name and drops it from the roster.
func (l *LocalAgents) Stop(name string) error {
	l.mu.Lock()
	var found *localProc
	for _, p := range l.procs {
		if p.name == name {
			found = p
			break
		}
	}
	l.mu.Unlock()
	if found == nil {
		return fmt.Errorf("local agent %s not found", name)
	}
	l.remove(found)
	killLocalProc(found.cmd)
	return nil
}

// StopAll kills every tracked local agent (hub shutdown), so none are left
// orphaned once the hub — which holds the only handle to them — exits.
func (l *LocalAgents) StopAll() {
	l.mu.Lock()
	all := l.procs
	l.procs = nil
	l.mu.Unlock()
	for _, p := range all {
		killLocalProc(p.cmd)
	}
}

// remove drops a specific tracked agent (called when its process exits).
func (l *LocalAgents) remove(p *localProc) {
	l.mu.Lock()
	defer l.mu.Unlock()
	for i, x := range l.procs {
		if x == p {
			l.procs = append(l.procs[:i], l.procs[i+1:]...)
			return
		}
	}
}

// view cross-references a child against the live pool by its display name.
func (l *LocalAgents) view(p *localProc) *types.LocalAgent {
	v := &types.LocalAgent{Name: p.name, Pid: int32(p.pid)}
	if l.pool == nil {
		return v
	}
	for _, a := range l.pool.List() {
		if a.GetHello().GetName() == p.name {
			v.Registered, v.Busy = true, a.GetBusy()
			break
		}
	}
	return v
}

// killLocalProc terminates a child process (best-effort; no-op once it exited).
func killLocalProc(cmd *exec.Cmd) {
	if cmd != nil && cmd.Process != nil {
		_ = cmd.Process.Kill()
	}
}
