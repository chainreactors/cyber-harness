package webagent

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/url"
	"os"
	"os/user"
	"runtime"
	"strings"
	"sync"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/runner"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/webproto"
	"github.com/chainreactors/utils/pty"
	"github.com/gorilla/websocket"
)

func Run(ctx context.Context, option *cfg.Option, logger telemetry.Logger) error {
	rt, err := runner.NewAgentRuntime(ctx, option, logger, &runner.RuntimeConfig{
		NoOutput:         true,
		IOA:              remoteIOAConfig(option),
		ProviderOptional: true,
	})
	if err != nil {
		return err
	}
	defer rt.Close()

	connectionDone := make(chan struct{})
	go func() {
		defer close(connectionDone)
		_ = rt.App.WaitEngines(ctx)
		logger.Debugf("web agent connection to %s", option.WebURL)
		_ = RunConnectionRuntime(ctx, option.WebURL, rt.NodeName, rt)
	}()

	if rt.App.Provider == nil {
		logger.Warnf("no LLM provider configured; remote REPL and PTY are available, autonomous agent loop is disabled")
		<-ctx.Done()
		<-connectionDone
		return nil
	}

	task, err := webAgentTask(option)
	if err != nil {
		return err
	}
	if task == "" {
		logger.Infof("web agent connected; remote REPL and PTY are available")
		<-ctx.Done()
		<-connectionDone
		return nil
	}

	loopCfg := rt.Config.WithSystemPrompt(rt.SystemPrompt).WithStream(true)
	_, err = agent.NewAgent(loopCfg).Run(ctx, task)

	<-connectionDone
	return err
}

func RunConnection(ctx context.Context, serverURL, name string, reg *commands.CommandRegistry, bus *eventbus.Bus[agent.Event]) error {
	return runConnection(ctx, serverURL, name, reg, bus, nil)
}

func RunConnectionRuntime(ctx context.Context, serverURL, name string, rt *runner.AgentRuntime) error {
	if rt == nil || rt.App == nil {
		return fmt.Errorf("agent runtime is not configured")
	}
	return runConnection(ctx, serverURL, name, rt.App.Commands, rt.Bus, rt)
}

func runConnection(ctx context.Context, serverURL, name string, reg *commands.CommandRegistry, bus *eventbus.Bus[agent.Event], rt *runner.AgentRuntime) error {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return nil //nolint:nilerr // intentional: suppress error on context cancellation
		}
		err := runConnectionOnce(ctx, serverURL, name, reg, bus, rt)
		if ctx.Err() != nil {
			return nil //nolint:nilerr // intentional: suppress error on context cancellation
		}
		if err != nil {
			delay := agent.RetryDelay(attempt)
			attempt++
			select {
			case <-ctx.Done():
				return nil
			case <-time.After(delay):
			}
		} else {
			attempt = 0
		}
	}
}

func runConnectionOnce(ctx context.Context, serverURL, name string, reg *commands.CommandRegistry, bus *eventbus.Bus[agent.Event], rt *runner.AgentRuntime) error {
	if reg == nil {
		return fmt.Errorf("command registry is nil")
	}
	wsURL := httpToWS(serverURL) + "/api/agent/ws"
	conn, wsResp, err := websocket.DefaultDialer.DialContext(ctx, wsURL, nil)
	if wsResp != nil && wsResp.Body != nil {
		wsResp.Body.Close()
	}
	if err != nil {
		return fmt.Errorf("ws dial: %w", err)
	}
	defer conn.Close()

	sendCh := make(chan webproto.Message, 64)
	done := make(chan struct{})
	defer close(done)

	send := func(m webproto.Message) {
		select {
		case sendCh <- m:
		case <-done:
		}
	}

	stats := newAgentStatsTracker()
	regPayload, _ := json.Marshal(agentRegisterPayload(name, reg, rt, stats.Snapshot()))
	if err := conn.WriteJSON(webproto.Message{Type: "register", Payload: regPayload}); err != nil {
		return fmt.Errorf("register: %w", err)
	}

	var ack webproto.Message
	if err := conn.ReadJSON(&ack); err != nil || ack.Type != "connected" {
		return fmt.Errorf("expected connected ack")
	}

	go func() {
		for {
			select {
			case msg, ok := <-sendCh:
				if !ok {
					return
				}
				_ = conn.WriteJSON(msg)
			case <-ctx.Done():
				_ = conn.WriteMessage(websocket.CloseMessage,
					websocket.FormatCloseMessage(websocket.CloseNormalClosure, ""))
				return
			case <-done:
				return
			}
		}
	}()

	go func() {
		select {
		case <-ctx.Done():
			conn.Close()
		case <-done:
		}
	}()

	var taskMu sync.Mutex
	tasks := make(map[string]context.CancelFunc)
	if bus != nil {
		unsub := bus.Subscribe(func(e agent.Event) {
			if next, ok := stats.Observe(e); ok {
				statsPayload, _ := json.Marshal(next)
				send(webproto.Message{Type: "agent.stats", Payload: statsPayload})
			}
			payload, _ := json.Marshal(e)
			data := agentEventSummary(e)
			if data == "" {
				data = string(payload)
			}
			taskMu.Lock()
			taskIDs := make([]string, 0, len(tasks))
			for taskID := range tasks {
				taskIDs = append(taskIDs, taskID)
			}
			taskMu.Unlock()
			for _, taskID := range taskIDs {
				send(webproto.Message{
					Type:    "agent." + string(e.Type),
					TaskID:  taskID,
					Data:    data,
					Payload: payload,
				})
			}
		})
		defer unsub()
	}

	ptyRouter := newPTYRouter(reg, rt)
	defer ptyRouter.Close()
	if mgr := registryPTYManager(reg); mgr != nil {
		unsub := subscribePTYSessions(ctx, mgr, ptyRouter, send)
		defer unsub()
	}

	for {
		var msg webproto.Message
		if err := conn.ReadJSON(&msg); err != nil {
			return err
		}
		if ctx.Err() != nil {
			return nil
		}

		if strings.HasPrefix(msg.Type, "pty.") {
			frame, err := webproto.MessageToFrame(msg)
			if err != nil {
				send(webproto.Message{Type: "pty.error", StreamID: msg.StreamID, Data: err.Error()})
				continue
			}
			ptyRouter.Handle(ctx, frame, func(out pty.Frame) {
				send(webproto.FrameToMessage(out))
			})
			continue
		}

		switch msg.Type {
		case "exec":
			taskCtx, cancel := context.WithCancel(ctx)
			taskMu.Lock()
			tasks[msg.TaskID] = cancel
			taskMu.Unlock()
			go func(m webproto.Message, tCtx context.Context, tCancel context.CancelFunc) {
				defer tCancel()
				defer func() {
					taskMu.Lock()
					delete(tasks, m.TaskID)
					taskMu.Unlock()
				}()
				execCommand(tCtx, m.TaskID, m.Data, reg, send)
			}(msg, taskCtx, cancel)

		case "cancel":
			taskMu.Lock()
			if cancel, ok := tasks[msg.TaskID]; ok {
				cancel()
			}
			taskMu.Unlock()
		}
	}
}

func newPTYRouter(reg *commands.CommandRegistry, rt *runner.AgentRuntime) *pty.Router {
	mgr := registryPTYManager(reg)
	var baseMgr *pty.Manager
	if mgr != nil {
		baseMgr = mgr.Manager
	}
	openers := pty.DefaultOpeners(baseMgr, pty.DefaultSessionTimeout, pty.DefaultEnv())
	if rt != nil {
		openers["repl"] = runner.NewRemoteREPLOpener(rt, mgr)
	}
	return pty.NewRouter(baseMgr, pty.WithOpeners(openers))
}

func registryPTYManager(reg *commands.CommandRegistry) *tmux.Manager {
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

func subscribePTYSessions(ctx context.Context, mgr *tmux.Manager, router *pty.Router, send func(webproto.Message)) func() {
	if mgr == nil || router == nil || send == nil {
		return func() {}
	}
	activity := newPTYActivityTracker()
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
				broadcastPTYSessions(mgr, router, activity, send)
			case <-ticker.C:
				if dirty {
					dirty = false
					broadcastPTYSessions(mgr, router, activity, send)
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

func broadcastPTYSessions(mgr *tmux.Manager, router *pty.Router, activity *ptyActivityTracker, send func(webproto.Message)) {
	streamIDs := router.StreamIDs()
	if len(streamIDs) == 0 {
		return
	}
	sessions := ptySessionViews(mgr.List(), activity)
	for _, streamID := range streamIDs {
		payload, _ := json.Marshal(map[string]any{"sessions": sessions})
		send(webproto.Message{Type: "pty.sessions", StreamID: streamID, Payload: payload})
	}
}

type ptyActivity struct {
	LastActivityAt time.Time `json:"last_activity_at,omitempty"`
	ActivitySeq    int64     `json:"activity_seq,omitempty"`
	OutputBytes    int64     `json:"output_bytes,omitempty"`
}

type ptyActivityTracker struct {
	mu       sync.Mutex
	sessions map[string]ptyActivity
}

type ptySessionView struct {
	tmux.Info
	LastActivityAt time.Time `json:"last_activity_at,omitempty"`
	ActivitySeq    int64     `json:"activity_seq,omitempty"`
	OutputBytes    int64     `json:"output_bytes,omitempty"`
}

func newPTYActivityTracker() *ptyActivityTracker {
	return &ptyActivityTracker{sessions: make(map[string]ptyActivity)}
}

func (t *ptyActivityTracker) Observe(ev tmux.Event) {
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

func (t *ptyActivityTracker) Snapshot(id string) ptyActivity {
	if t == nil || id == "" {
		return ptyActivity{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.sessions[id]
}

func ptySessionViews(sessions []tmux.Info, activity *ptyActivityTracker) []ptySessionView {
	views := make([]ptySessionView, 0, len(sessions))
	for _, session := range sessions {
		snapshot := activity.Snapshot(session.ID)
		if snapshot.LastActivityAt.IsZero() {
			snapshot.LastActivityAt = session.EndedAt
		}
		if snapshot.LastActivityAt.IsZero() {
			snapshot.LastActivityAt = session.StartedAt
		}
		views = append(views, ptySessionView{
			Info:           session,
			LastActivityAt: snapshot.LastActivityAt,
			ActivitySeq:    snapshot.ActivitySeq,
			OutputBytes:    snapshot.OutputBytes,
		})
	}
	return views
}

func execCommand(ctx context.Context, taskID, cmdLine string, reg *commands.CommandRegistry, send func(webproto.Message)) {
	tokens, err := commands.SplitCommandLine(cmdLine)
	if err != nil {
		send(webproto.Message{Type: "error", TaskID: taskID, Data: err.Error()})
		return
	}
	if len(tokens) == 0 {
		send(webproto.Message{Type: "error", TaskID: taskID, Data: "empty command"})
		return
	}

	writer := &streamWriter{taskID: taskID, sendFn: send}

	if cmd, ok := reg.Get(tokens[0]); ok {
		if sc, ok := cmd.(interface {
			ExecuteStructured(ctx context.Context, args []string, stream io.Writer) (string, *output.Result, error)
		}); ok {
			out, result, err := sc.ExecuteStructured(ctx, tokens[1:], writer)
			writer.flush()
			if err != nil {
				send(webproto.Message{Type: "error", TaskID: taskID, Data: err.Error()})
				return
			}
			var payload json.RawMessage
			if result != nil {
				payload, _ = json.Marshal(result)
			}
			send(webproto.Message{Type: "complete", TaskID: taskID, Data: out, Payload: payload})
			return
		}
	}

	out, err := reg.ExecuteArgsStreaming(ctx, tokens, writer)
	writer.flush()
	if err != nil {
		send(webproto.Message{Type: "error", TaskID: taskID, Data: err.Error()})
		return
	}
	send(webproto.Message{Type: "complete", TaskID: taskID, Data: out})
}

type agentStatsTracker struct {
	mu    sync.Mutex
	stats webproto.AgentStats
}

func newAgentStatsTracker() *agentStatsTracker {
	return &agentStatsTracker{}
}

func (t *agentStatsTracker) Snapshot() webproto.AgentStats {
	if t == nil {
		return webproto.AgentStats{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.stats
}

func (t *agentStatsTracker) Observe(e agent.Event) (webproto.AgentStats, bool) {
	if t == nil {
		return webproto.AgentStats{}, false
	}
	t.mu.Lock()
	defer t.mu.Unlock()

	t.stats.LastEvent = string(e.Type)
	switch e.Type {
	case agent.EventTurnEnd:
		if e.Turn > t.stats.Turns {
			t.stats.Turns = e.Turn
		}
		if e.Usage != nil {
			t.stats.PromptTokens += e.Usage.PromptTokens
			t.stats.CompletionTokens += e.Usage.CompletionTokens
			t.stats.TotalTokens += e.Usage.TotalTokens
			t.stats.CacheReadTokens += e.Usage.CacheReadTokens
			t.stats.CacheWriteTokens += e.Usage.CacheWriteTokens
		}
	case agent.EventToolExecutionStart:
		t.stats.ToolCalls++
		t.stats.RunningTools++
	case agent.EventToolExecutionEnd:
		if t.stats.RunningTools > 0 {
			t.stats.RunningTools--
		}
	default:
		return t.stats, false
	}
	return t.stats, true
}

func agentRegisterPayload(name string, reg *commands.CommandRegistry, rt *runner.AgentRuntime, stats webproto.AgentStats) webproto.RegisterPayload {
	payload := webproto.RegisterPayload{
		Name:     name,
		Commands: reg.Names(),
		Stats:    stats,
		Identity: agentIdentity(rt),
	}
	if payload.Identity.NodeName == "" {
		payload.Identity.NodeName = name
	}
	return payload
}

func agentIdentity(rt *runner.AgentRuntime) webproto.AgentIdentity {
	identity := webproto.AgentIdentity{
		OS:           runtime.GOOS,
		Arch:         runtime.GOARCH,
		PID:          os.Getpid(),
		Capabilities: []string{"repl", "pty", "tmux", "ioa"},
		Meta:         map[string]any{"client": "aiscan", "transport": "web-agent"},
	}
	if host, err := os.Hostname(); err == nil {
		identity.Hostname = host
	}
	if wd, err := os.Getwd(); err == nil {
		identity.WorkingDir = wd
	}
	if current, err := user.Current(); err == nil && current != nil {
		identity.Username = current.Username
	}
	if rt == nil {
		return identity
	}
	identity.NodeName = rt.NodeName
	if rt.Option != nil {
		identity.Space = rt.Option.Space
		identity.IOAURL = publicIOAURL(rt.Option.IOAURL)
	}
	if rt.App != nil {
		if rt.App.IOAClient != nil {
			identity.NodeID = rt.App.IOAClient.NodeID()
		}
		identity.Provider = rt.App.ProviderConfig.Provider
		identity.Model = rt.App.ProviderConfig.Model
	}
	return identity
}

func publicIOAURL(raw string) string {
	if raw == "" {
		return ""
	}
	parsed, err := url.Parse(strings.TrimRight(raw, "/"))
	if err != nil {
		return raw
	}
	parsed.User = nil
	return parsed.String()
}

func agentEventSummary(e agent.Event) string {
	switch e.Type {
	case agent.EventToolExecutionStart:
		return e.ToolName
	case agent.EventToolExecutionEnd:
		if e.IsError {
			return e.ToolName + " error"
		}
		return e.ToolName + " done"
	case agent.EventTurnStart:
		return fmt.Sprintf("turn %d", e.Turn)
	case agent.EventTurnEnd:
		if e.Usage != nil {
			return fmt.Sprintf("turn %d tokens=%d", e.Turn, e.Usage.TotalTokens)
		}
		return fmt.Sprintf("turn %d", e.Turn)
	default:
		return ""
	}
}

const maxStreamBuf = 64 << 10

type streamWriter struct {
	taskID string
	sendFn func(webproto.Message)
	buf    []byte
}

func (w *streamWriter) Write(p []byte) (int, error) {
	w.buf = append(w.buf, p...)
	for {
		idx := bytes.IndexByte(w.buf, '\n')
		if idx < 0 {
			if len(w.buf) >= maxStreamBuf {
				w.flush()
			}
			break
		}
		line := string(w.buf[:idx])
		w.buf = w.buf[idx+1:]
		if strings.TrimSpace(line) == "" {
			continue
		}
		w.sendFn(webproto.Message{Type: "output", TaskID: w.taskID, Data: line})
	}
	return len(p), nil
}

func (w *streamWriter) flush() {
	if len(w.buf) == 0 {
		return
	}
	data := string(w.buf)
	w.buf = w.buf[:0]
	if strings.TrimSpace(data) != "" {
		w.sendFn(webproto.Message{Type: "output", TaskID: w.taskID, Data: data})
	}
}

func webAgentTask(option *cfg.Option) (string, error) {
	if option == nil {
		return "", nil
	}
	if strings.TrimSpace(option.Prompt) == "" && option.TaskFile == "" && len(option.Inputs) == 0 {
		return "", nil
	}
	return cfg.ResolveTask(option)
}

func remoteIOAConfig(option *cfg.Option) *cfg.IOAConfig {
	if option == nil || option.IOAURL == "" {
		return nil
	}
	return &cfg.IOAConfig{
		URL:           option.IOAURL,
		NodeID:        option.IOANodeID,
		NodeName:      option.IOANodeName,
		Space:         option.Space,
		RegisterTools: true,
		AutoRegister:  true,
		NodeMeta:      map[string]any{"client": "aiscan", "transport": "web-agent"},
	}
}

func httpToWS(rawURL string) string {
	u, err := url.Parse(strings.TrimRight(rawURL, "/"))
	if err != nil {
		return rawURL
	}
	switch u.Scheme {
	case "https":
		u.Scheme = "wss"
	default:
		u.Scheme = "ws"
	}
	return u.String()
}
