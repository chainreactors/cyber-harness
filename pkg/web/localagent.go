package web

import (
	"context"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"strings"
	"sync"
)

// LocalAgentView is the API-facing view of a hub-hosted agent, cross-referenced
// with the live pool for connection state.
type LocalAgentView struct {
	Name       string `json:"name"`
	PID        int    `json:"pid"`
	Registered bool   `json:"registered"` // has connected back to the hub pool
	Busy       bool   `json:"busy,omitempty"`
}

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
	webURL     string     // hub loopback address children dial (derived from web --addr)
	webAuthURL string     // same base with the access token as userinfo, for /api/agent/ws auth
	ioaURL     string     // hub IOA endpoint carrying the embedded access token
	configFile string     // explicit hub config inherited by hub-launched children
	pool       *AgentPool // live pool, for registration/busy cross-reference

	mu    sync.Mutex
	procs []*localProc
	seq   int
}

// NewLocalAgents builds a launcher. hubURL is the loopback base the children
// dial (e.g. http://127.0.0.1:8080); ioaToken is embedded into the child's IOA
// URL. Children are launched from the current aiscan executable.
func NewLocalAgents(hubURL, ioaToken, configFile string, pool *AgentPool) *LocalAgents {
	return &LocalAgents{
		webURL:     hubURL,
		webAuthURL: webURLWithToken(hubURL, ioaToken),
		ioaURL:     nodeIOAURL(hubURL, ioaToken),
		configFile: strings.TrimSpace(configFile),
		pool:       pool,
	}
}

// webURLWithToken embeds the access token as userinfo on the hub's loopback web
// URL (http://<token>@host), so a launched agent can authenticate its
// /api/agent/ws pool connection — the hub gates /api/* behind that key. An empty
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

// nodeIOAURL embeds the access token as userinfo and points at the /ioa path,
// yielding http://<token>@host:port/ioa. An empty or unparseable hubURL yields "".
func nodeIOAURL(hubURL, token string) string {
	if hubURL == "" {
		return ""
	}
	u, err := url.Parse(strings.TrimRight(hubURL, "/"))
	if err != nil {
		return ""
	}
	if token != "" {
		u.User = url.User(token)
	}
	u.Path = "/ioa"
	return u.String()
}

// Launch spawns an `aiscan agent` on the hub host wired to the hub's loopback
// web + IOA endpoints, and tracks it. The LLM provider/model/key arrive via the
// hub's config push on registration, so nothing about the model is passed here.
func (l *LocalAgents) Launch(ctx context.Context) (*LocalAgentView, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if l.webURL == "" {
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
		"--web-url", l.webAuthURL,
		"--server-url", l.ioaURL,
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
	return &v, nil
}

// List returns the tracked local agents (launch order), cross-referenced with
// the pool for connection state.
func (l *LocalAgents) List() []LocalAgentView {
	l.mu.Lock()
	all := make([]*localProc, len(l.procs))
	copy(all, l.procs)
	l.mu.Unlock()

	views := make([]LocalAgentView, 0, len(all))
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
func (l *LocalAgents) view(p *localProc) LocalAgentView {
	v := LocalAgentView{Name: p.name, PID: p.pid}
	if l.pool == nil {
		return v
	}
	for _, a := range l.pool.List() {
		if a.Name == p.name {
			v.Registered, v.Busy = true, a.Busy
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

// ---------------------------------------------------------------------------
// HTTP surface
// ---------------------------------------------------------------------------

func (l *LocalAgents) handleLaunch(w http.ResponseWriter, r *http.Request) {
	view, err := l.Launch(r.Context())
	if err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, view)
}

func (l *LocalAgents) handleList(w http.ResponseWriter, r *http.Request) {
	writeJSON(w, http.StatusOK, l.List())
}

func (l *LocalAgents) handleStop(w http.ResponseWriter, r *http.Request) {
	if err := l.Stop(r.PathValue("id")); err != nil {
		writeError(w, http.StatusUnprocessableEntity, err.Error())
		return
	}
	writeJSON(w, http.StatusOK, map[string]string{"status": "stopped"})
}

// registerLocalAgentRoutes wires the hub-hosted local-agent endpoints. The
// literal "local" segment never collides with a real id, so plain paths suffice.
func registerLocalAgentRoutes(mux *http.ServeMux, l *LocalAgents) {
	if l == nil {
		return
	}
	mux.HandleFunc("POST /api/deploy/local", l.handleLaunch)
	mux.HandleFunc("GET /api/deploy/local", l.handleList)
	mux.HandleFunc("DELETE /api/deploy/local/{id}", l.handleStop)
}
