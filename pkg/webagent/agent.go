package webagent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"os/user"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/runner"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/evaluator"
	"github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tui"
	"github.com/chainreactors/aiscan/pkg/webproto"
	"github.com/chainreactors/utils/pty"
	"github.com/gorilla/websocket"
)

func Run(ctx context.Context, option *cfg.Option, logger telemetry.Logger) error {
	if option.WebURL != "" {
		remoteOpt, err := cfg.FetchRemoteConfig(option.WebURL)
		if err != nil {
			logger.Warnf("fetch remote config from %s: %s (continuing with local config)", option.WebURL, err)
		} else {
			logger.Infof("fetched remote config from %s", option.WebURL)
			cfg.MergeRemoteOption(option, remoteOpt)
		}
	}

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
	return runConnection(ctx, serverURL, defaultWSPath, name, reg, bus, nil)
}

func RunConnectionWithPath(ctx context.Context, serverURL, wsPath, name string, reg *commands.CommandRegistry, bus *eventbus.Bus[agent.Event]) error {
	return runConnection(ctx, serverURL, wsPath, name, reg, bus, nil)
}

// ConnectionConfig provides extended options for RunConnectionWithPathEx.
type ConnectionConfig struct {
	ServerURL string
	WSPath    string
	Name      string
	Registry  *commands.CommandRegistry
	AgentBus  *eventbus.Bus[agent.Event]
	DataBus   *eventbus.Bus[output.ToolDataEvent]
	SCO       *output.SCOSidecar
}

// RunConnectionWithPathEx connects with full data pipeline support — ToolDataEvents
// and SCO nodes are forwarded over the WebSocket as "tool.data" and "tool.sco" messages.
func RunConnectionWithPathEx(ctx context.Context, cc ConnectionConfig) error {
	if cc.WSPath == "" {
		cc.WSPath = defaultWSPath
	}
	pipeline := buildDataPipeline(cc.DataBus, cc.SCO)
	attempt := 0
	for {
		if ctx.Err() != nil {
			return nil
		}
		err := runConnectionOnceWithPipeline(ctx, cc.ServerURL, cc.WSPath, cc.Name, cc.Registry, cc.AgentBus, nil, pipeline)
		if ctx.Err() != nil {
			return nil
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

// dataPipeline subscribes to DataBus/SCO and forwards events over WS.
type dataPipeline struct {
	dataBus *eventbus.Bus[output.ToolDataEvent]
	sco     *output.SCOSidecar
	unsub   func()
}

func buildDataPipeline(db *eventbus.Bus[output.ToolDataEvent], sco *output.SCOSidecar) *dataPipeline {
	if db == nil && sco == nil {
		return nil
	}
	return &dataPipeline{dataBus: db, sco: sco}
}

func (p *dataPipeline) attach(send func(webproto.Message)) {
	if p == nil {
		return
	}
	if p.dataBus != nil {
		p.unsub = p.dataBus.Subscribe(func(ev output.ToolDataEvent) {
			payload, _ := json.Marshal(ev)
			send(webproto.Message{Type: "tool.data", Payload: payload})
		})
	}
	if p.sco != nil {
		p.sco.OnNodes = func(callID string, nodes []json.RawMessage) {
			payload, _ := json.Marshal(map[string]any{"call_id": callID, "nodes": nodes})
			send(webproto.Message{Type: "tool.sco", Payload: payload})
		}
	}
}

func (p *dataPipeline) detach() {
	if p == nil {
		return
	}
	if p.unsub != nil {
		p.unsub()
		p.unsub = nil
	}
	if p.sco != nil {
		p.sco.OnNodes = nil
	}
}

func RunConnectionRuntime(ctx context.Context, serverURL, name string, rt *runner.AgentRuntime) error {
	if rt == nil || rt.App == nil {
		return fmt.Errorf("agent runtime is not configured")
	}
	return runConnection(ctx, serverURL, defaultWSPath, name, rt.App.Commands, rt.Bus, rt)
}

const defaultWSPath = "/api/agent/ws"

func runConnection(ctx context.Context, serverURL, wsPath, name string, reg *commands.CommandRegistry, bus *eventbus.Bus[agent.Event], rt *runner.AgentRuntime) error {
	attempt := 0
	for {
		if ctx.Err() != nil {
			return nil //nolint:nilerr // intentional: suppress error on context cancellation
		}
		err := runConnectionOnceWithPath(ctx, serverURL, wsPath, name, reg, bus, rt)
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

func runConnectionOnceWithPath(ctx context.Context, serverURL, wsPath, name string, reg *commands.CommandRegistry, bus *eventbus.Bus[agent.Event], rt *runner.AgentRuntime) error {
	return runConnectionOnceWithPipeline(ctx, serverURL, wsPath, name, reg, bus, rt, nil)
}

func runConnectionOnceWithPipeline(ctx context.Context, serverURL, wsPath, name string, reg *commands.CommandRegistry, bus *eventbus.Bus[agent.Event], rt *runner.AgentRuntime, pipeline *dataPipeline) error {
	if reg == nil {
		return fmt.Errorf("command registry is nil")
	}
	dialURL, accessKey := splitAccessKey(serverURL)
	wsURL := httpToWS(dialURL) + wsPath
	var reqHeader http.Header
	if accessKey != "" {
		reqHeader = http.Header{"Authorization": {"Bearer " + accessKey}}
	}
	conn, wsResp, err := websocket.DefaultDialer.DialContext(ctx, wsURL, reqHeader)
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

	if pipeline != nil {
		pipeline.attach(send)
		defer pipeline.detach()
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

	var mu sync.Mutex
	execTasks := make(map[string]context.CancelFunc)   // tmux-managed exec tasks
	chatCancels := make(map[string]context.CancelFunc) // active chat messageID → cancel
	eventRoute := make(map[string]string)              // agent SessionID → messageID for event routing
	if bus != nil {
		unsub := bus.Subscribe(func(e agent.Event) {
			if next, ok := stats.Observe(e); ok {
				statsPayload, _ := json.Marshal(next)
				send(webproto.Message{Type: "agent.stats", Payload: statsPayload})
			}
			rec := output.NewRecord(output.TypeAgent, e)
			payload, _ := json.Marshal(rec)
			data := agentEventSummary(e)
			if data == "" {
				data = string(payload)
			}
			mu.Lock()
			msgID := eventRoute[e.SessionID]
			if msgID == "" && e.ParentSessionID != "" {
				msgID = eventRoute[e.ParentSessionID]
				if msgID != "" {
					eventRoute[e.SessionID] = msgID
				}
			}
			var targets []string
			if msgID != "" {
				targets = []string{msgID}
			} else {
				for tid := range execTasks {
					targets = append(targets, tid)
				}
			}
			mu.Unlock()
			for _, id := range targets {
				send(webproto.Message{
					Type:    "agent." + string(e.Type),
					TaskID:  id,
					Data:    data,
					Payload: payload,
				})
			}
		})
		defer unsub()
	}

	ptyRouter := newPTYRouter(reg, rt)
	defer ptyRouter.Close()
	chatRuntime := newChatRuntimeManager(rt)
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
			mu.Lock()
			execTasks[msg.TaskID] = cancel
			mu.Unlock()
			go func(m webproto.Message, tCtx context.Context, tCancel context.CancelFunc) {
				defer tCancel()
				defer func() {
					mu.Lock()
					delete(execTasks, m.TaskID)
					mu.Unlock()
				}()
				execCommand(tCtx, m, reg, send)
			}(msg, taskCtx, cancel)

		case "chat":
			chatOpts := parseChatPayload(msg)
			webSessionID := chatOpts.SessionID
			ag, agErr := chatRuntime.agentFor(webSessionID)
			if agErr != nil {
				send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: agErr.Error()})
				continue
			}

			prompt := strings.TrimSpace(msg.Data)
			if prompt == "" {
				send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: "empty prompt"})
				continue
			}

			// Always route future events to the latest message.
			mu.Lock()
			eventRoute[ag.Cfg.SessionID] = msg.TaskID
			mu.Unlock()

			if ag.IsRunning() {
				// Agent is busy — append to inbox; the loop picks it up. Leave any
				// pending upload notes queued so they ride the next idle turn rather
				// than being drained into a steer that may not surface them.
				ag.SteerUserMessage(prompt)
				send(webproto.Message{Type: "complete", TaskID: msg.TaskID})
				continue
			}

			// Idle turn: fold in files uploaded to this session since the last turn so
			// the agent learns their absolute on-disk paths and can read them. REPL/`!`
			// lines are left untouched so a note never corrupts a command; the note
			// stays queued for the next natural-language turn.
			if !isREPLCommand(prompt) {
				if note := chatRuntime.takePendingUploads(webSessionID); note != "" {
					msg.Data = note + "\n\n" + prompt
				}
			}

			// Agent is idle — start a new run with this message.
			chatCtx, chatCancel := context.WithCancel(ctx)
			mu.Lock()
			chatCancels[msg.TaskID] = chatCancel
			mu.Unlock()
			go func(m webproto.Message, cCtx context.Context, cCancel context.CancelFunc) {
				defer cCancel()
				defer func() {
					mu.Lock()
					delete(chatCancels, m.TaskID)
					for sid, mid := range eventRoute {
						if mid == m.TaskID {
							delete(eventRoute, sid)
						}
					}
					mu.Unlock()
				}()
				runChatWithAgent(cCtx, m, chatOpts, ag, rt, send)
			}(msg, chatCtx, chatCancel)

		case "upload":
			go handleFileUpload(msg, send, chatRuntime)

		case "file.read":
			go handleFileRead(msg, send)

		case "file.write":
			go handleFileWrite(msg, send)

		case "config":
			// Hub pushed a config change (LLM provider/model/key). Re-fetch and
			// hot-swap the provider off the read loop so a slow fetch never
			// stalls it; reloadProvider serializes concurrent pushes. On success,
			// re-announce identity so the hub/UI reflect the swapped provider/model —
			// identity is otherwise sent only once, at registration, so its badge
			// would keep showing the pre-reload model.
			go func() {
				if provider, model, ok := reloadAgentConfig(serverURL, rt, chatRuntime); ok {
					payload, _ := json.Marshal(webproto.AgentIdentity{Provider: provider.Name(), Model: model})
					send(webproto.Message{Type: "agent.identity", Payload: payload})
				}
			}()

		case "cancel":
			mu.Lock()
			if cancel, ok := execTasks[msg.TaskID]; ok {
				cancel()
			} else if cancel, ok := chatCancels[msg.TaskID]; ok {
				cancel()
			}
			mu.Unlock()
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

func execCommand(ctx context.Context, msg webproto.Message, reg *commands.CommandRegistry, send func(webproto.Message)) {
	taskID := msg.TaskID

	// Parse structured payload; fall back to Data for backward compat.
	var ep webproto.ExecPayload
	if len(msg.Payload) > 0 {
		_ = json.Unmarshal(msg.Payload, &ep)
	}
	if ep.Command == "" {
		ep.Command = strings.TrimSpace(msg.Data)
	}
	if ep.Command == "" {
		send(webproto.Message{Type: "error", TaskID: taskID, Data: "empty command"})
		return
	}

	if ep.Cwd != "" {
		reg.SetWorkDir(ep.Cwd)
	}

	// Scope scanner telemetry/SCO output to the Cairn RPC that launched it.
	// The bridge uses this call id to associate discovered assets with the
	// task/tenant that owns the exec request.
	execCtx := output.ContextWithCallID(ctx, taskID)
	if ep.Timeout > 0 {
		var cancel context.CancelFunc
		execCtx, cancel = context.WithTimeout(ctx, time.Duration(ep.Timeout)*time.Second)
		defer cancel()
	}

	tokens, err := commands.SplitCommandLine(ep.Command)
	if err != nil {
		send(webproto.Message{Type: "error", TaskID: taskID, Data: err.Error()})
		return
	}
	if len(tokens) == 0 {
		send(webproto.Message{Type: "error", TaskID: taskID, Data: "empty command"})
		return
	}

	writer := &streamWriter{taskID: taskID, sendFn: send}

	// Try registered command (aiscan tools: scan, gogo, spray, etc.)
	if cmd, ok := reg.Get(tokens[0]); ok {
		if sc, ok := cmd.(interface {
			ExecuteStructured(ctx context.Context, args []string, stream io.Writer) (string, *output.Result, error)
		}); ok {
			out, result, err := sc.ExecuteStructured(execCtx, tokens[1:], writer)
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
		out, err := reg.ExecuteArgsStreaming(execCtx, tokens, writer)
		writer.flush()
		if err != nil {
			send(webproto.Message{Type: "error", TaskID: taskID, Data: err.Error()})
			return
		}
		send(webproto.Message{Type: "complete", TaskID: taskID, Data: out})
		return
	}

	// Shell fallback — preserve Cairn's exec contract for cwd, env, timeout,
	// streaming output, and the real process exit code.
	shellExec(execCtx, taskID, ep, send)
}

func shellExec(ctx context.Context, taskID string, ep webproto.ExecPayload, send func(webproto.Message)) {
	var cmd *exec.Cmd
	if runtime.GOOS == "windows" {
		cmd = exec.CommandContext(ctx, "cmd", "/c", ep.Command)
	} else {
		cmd = exec.CommandContext(ctx, "sh", "-c", ep.Command)
	}
	if ep.Cwd != "" {
		cmd.Dir = ep.Cwd
	}
	cmd.Env = os.Environ()
	for key, value := range ep.Env {
		cmd.Env = append(cmd.Env, key+"="+value)
	}

	out, err := cmd.CombinedOutput()
	if len(out) > 0 {
		send(webproto.Message{Type: "output", TaskID: taskID, Data: string(out)})
	}
	if err != nil {
		code := 1
		if exitErr, ok := err.(*exec.ExitError); ok {
			code = exitErr.ExitCode()
		}
		send(webproto.Message{Type: "complete", TaskID: taskID, Data: fmt.Sprintf("exit %d", code)})
		return
	}
	send(webproto.Message{Type: "complete", TaskID: taskID})
}

// parseChatPayload decodes the "chat" WS payload: the web session to scope the
// agent conversation to, plus optional Goal-mode run controls.
func parseChatPayload(msg webproto.Message) webproto.ChatPayload {
	var payload webproto.ChatPayload
	if len(msg.Payload) > 0 {
		_ = json.Unmarshal(msg.Payload, &payload)
	}
	payload.SessionID = strings.TrimSpace(payload.SessionID)
	payload.EvalCriteria = strings.TrimSpace(payload.EvalCriteria)
	return payload
}

type chatRuntimeManager struct {
	rt       *runner.AgentRuntime
	mu       sync.Mutex
	sessions map[string]*agent.Agent

	uploadMu sync.Mutex
	uploads  map[string][]string // web sessionID → notes about files uploaded since the last turn
}

func newChatRuntimeManager(rt *runner.AgentRuntime) *chatRuntimeManager {
	return &chatRuntimeManager{
		rt:       rt,
		sessions: make(map[string]*agent.Agent),
		uploads:  make(map[string][]string),
	}
}

// notePendingUpload records that a file was written to the agent's local disk for
// a web session. The hub's SysFileUploaded broadcast only reaches the UI, so the
// LLM never learns the path on its own; the note is folded into the session's next
// natural-language turn (see the "chat" dispatch) so "read the file" resolves to
// the real absolute path instead of a bare filename against the cwd.
func (m *chatRuntimeManager) notePendingUpload(sessionID, note string) {
	if m == nil || note == "" {
		return
	}
	if sessionID == "" {
		sessionID = "default"
	}
	m.uploadMu.Lock()
	m.uploads[sessionID] = append(m.uploads[sessionID], note)
	m.uploadMu.Unlock()
}

// takePendingUploads drains and joins the pending upload notes for a session,
// returning "" when there are none. Draining is one-shot so each note reaches
// exactly one turn. The empty session ID normalizes to "default" to match agentFor.
func (m *chatRuntimeManager) takePendingUploads(sessionID string) string {
	if m == nil {
		return ""
	}
	if sessionID == "" {
		sessionID = "default"
	}
	m.uploadMu.Lock()
	notes := m.uploads[sessionID]
	delete(m.uploads, sessionID)
	m.uploadMu.Unlock()
	return strings.Join(notes, "\n")
}

func (m *chatRuntimeManager) agentFor(sessionID string) (*agent.Agent, error) {
	if m == nil || m.rt == nil || m.rt.App == nil {
		return nil, fmt.Errorf("agent runtime is not configured")
	}
	if sessionID == "" {
		sessionID = "default"
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	if ag := m.sessions[sessionID]; ag != nil {
		return ag, nil
	}
	ag := agent.NewAgent(m.rt.Config.
		WithSystemPrompt(m.rt.SystemPrompt).
		WithStream(true).
		WithInbox(nil))
	m.sessions[sessionID] = ag
	return ag, nil
}

// reloadProvider rebuilds the LLM provider from option and hot-swaps it across
// the runtime template (rt.App + rt.Config) and every live session, all under
// m.mu so a concurrent agentFor never clones a half-updated template. A run
// already in flight finishes on its old provider; the next message uses the new
// one.
func (m *chatRuntimeManager) reloadProvider(option *cfg.Option) (agent.Provider, string, error) {
	if m == nil || m.rt == nil {
		return nil, "", fmt.Errorf("agent runtime is not configured")
	}
	m.mu.Lock()
	defer m.mu.Unlock()
	provider, model, err := m.rt.ReloadProvider(option)
	if err != nil {
		return nil, "", err
	}
	for _, ag := range m.sessions {
		ag.SetProvider(provider, model)
	}
	return provider, model, nil
}

// reloadAgentConfig re-fetches the hub config and hot-swaps the LLM provider so
// a running agent picks up a Settings change without a restart. Best-effort: a
// fetch/build failure leaves the current provider in place. serverURL is the hub
// base the agent already dials. Returns the live provider, resolved model, and
// true when the swap succeeded, so the caller can re-announce identity.
func reloadAgentConfig(serverURL string, rt *runner.AgentRuntime, cr *chatRuntimeManager) (agent.Provider, string, bool) {
	if rt == nil {
		return nil, "", false
	}
	logger := rt.Config.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	remoteOpt, err := cfg.FetchRemoteConfig(serverURL)
	if err != nil {
		logger.Warnf("config reload: fetch remote config: %s", err)
		return nil, "", false
	}
	provider, model, err := cr.reloadProvider(remoteOpt)
	if err != nil {
		logger.Warnf("config reload: rebuild provider: %s", err)
		return nil, "", false
	}
	logger.Importantf("config reloaded: provider=%s model=%s", provider.Name(), model)
	return provider, model, true
}

func runChatWithAgent(ctx context.Context, msg webproto.Message, opts webproto.ChatPayload, ag *agent.Agent, rt *runner.AgentRuntime, send func(webproto.Message)) {
	prompt := strings.TrimSpace(msg.Data)
	if rt == nil || rt.App == nil {
		send(webproto.Message{
			Type:   "error",
			TaskID: msg.TaskID,
			Data:   "LLM provider is not configured on this agent; configure aiscan.yaml and restart the agent, or prefix commands with !",
		})
		return
	}

	if isREPLCommand(prompt) {
		out, err := runChatREPLLine(ctx, prompt, rt, ag)
		if err != nil {
			send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: err.Error()})
			return
		}
		send(webproto.Message{Type: "complete", TaskID: msg.TaskID, Data: out})
		return
	}

	if rt.App.Provider == nil {
		send(webproto.Message{
			Type:   "error",
			TaskID: msg.TaskID,
			Data:   "LLM provider is not configured on this agent; configure aiscan.yaml and restart the agent, or prefix commands with !",
		})
		return
	}

	// Goal "达成条件" mode: run the agent under an independent evaluator that
	// judges the natural-language criteria each round and re-drives the agent
	// with feedback until it passes (or the round budget is spent).
	if opts.EvalCriteria != "" {
		ag.SetMaxTurns(rt.Config.MaxTurns) // each eval round runs to natural completion
		runChatEval(ctx, msg, prompt, opts, ag, rt, send)
		return
	}

	// Goal "固定轮次" mode caps this run at PersistMaxTurns; otherwise restore
	// the session default so a prior capped message never leaks its cap forward.
	if opts.PersistMaxTurns > 0 {
		ag.SetMaxTurns(opts.PersistMaxTurns)
	} else {
		ag.SetMaxTurns(rt.Config.MaxTurns)
	}

	result, err := ag.Run(ctx, prompt)
	if err != nil {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: err.Error()})
		return
	}
	if result == nil {
		send(webproto.Message{Type: "complete", TaskID: msg.TaskID})
		return
	}
	send(webproto.Message{Type: "complete", TaskID: msg.TaskID, Data: trimChatOutput(result.Output)})
}

// runChatEval drives the agent through the evaluator loop for a Goal with
// natural-language acceptance criteria, using the agent's own provider/model as
// the independent judge. The final agent output is returned as the chat reply;
// per-round progress streams over rt.Bus like any other agent run.
func runChatEval(ctx context.Context, msg webproto.Message, prompt string, opts webproto.ChatPayload, ag *agent.Agent, rt *runner.AgentRuntime, send func(webproto.Message)) {
	evalCfg := evaluator.EvalLoopConfig{
		Evaluator: evaluator.New(evaluator.Config{
			Provider: rt.App.Provider,
			Model:    rt.Config.Model,
			Logger:   rt.Config.Logger,
		}),
		MaxEvalRounds: opts.EvalMaxRounds,
		Goal:          prompt,
		Criteria:      opts.EvalCriteria,
		Bus:           rt.Bus,
	}
	result, _, err := evaluator.RunWithEval(ctx, ag, evalCfg)
	if err != nil {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: err.Error()})
		return
	}
	if result == nil {
		send(webproto.Message{Type: "complete", TaskID: msg.TaskID})
		return
	}
	send(webproto.Message{Type: "complete", TaskID: msg.TaskID, Data: trimChatOutput(result.Output)})
}

func handleFileUpload(msg webproto.Message, send func(webproto.Message), cr *chatRuntimeManager) {
	var payload webproto.FileUploadPayload
	if len(msg.Payload) > 0 {
		_ = json.Unmarshal(msg.Payload, &payload)
	}
	if payload.Filename == "" {
		payload.Filename = "upload"
	}

	data, err := base64.StdEncoding.DecodeString(msg.DataB64)
	if err != nil {
		send(webproto.Message{
			Type:    "complete",
			TaskID:  msg.TaskID,
			Payload: webproto.MustJSON(webproto.FileUploadResult{Filename: payload.Filename, Error: "decode failed: " + err.Error()}),
		})
		return
	}

	dir := filepath.Join(os.TempDir(), "aiscan-uploads")
	_ = os.MkdirAll(dir, 0o755)
	dest := filepath.Join(dir, payload.Filename)

	if err := os.WriteFile(dest, data, 0o644); err != nil {
		send(webproto.Message{
			Type:    "complete",
			TaskID:  msg.TaskID,
			Payload: webproto.MustJSON(webproto.FileUploadResult{Filename: payload.Filename, Error: "write failed: " + err.Error()}),
		})
		return
	}

	// Surface the absolute on-disk path to the agent's next turn. Without this the
	// LLM only ever sees the hub's UI-only "file uploaded" notice and, asked to read
	// the file, guesses the bare filename against its cwd — which is not the upload dir.
	cr.notePendingUpload(payload.SessionID, fmt.Sprintf(
		"[已上传文件] 名称=%q 大小=%d 字节 · agent 本地绝对路径: %s\n（该文件已保存在 agent 磁盘上，需要查看内容时用 read 工具打开上述绝对路径。）",
		payload.Filename, len(data), dest))

	send(webproto.Message{
		Type:   "complete",
		TaskID: msg.TaskID,
		Data:   dest,
		Payload: webproto.MustJSON(webproto.FileUploadResult{
			Filename: payload.Filename,
			Path:     dest,
			Size:     int64(len(data)),
		}),
	})
}

func handleFileRead(msg webproto.Message, send func(webproto.Message)) {
	var payload webproto.FileRPCPayload
	if len(msg.Payload) > 0 {
		_ = json.Unmarshal(msg.Payload, &payload)
	}
	payload.Path = strings.TrimSpace(payload.Path)
	if payload.Path == "" {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: "file path required"})
		return
	}

	data, err := os.ReadFile(payload.Path)
	if err != nil {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: err.Error()})
		return
	}
	payload.Size = int64(len(data))
	send(webproto.Message{
		Type:    "complete",
		TaskID:  msg.TaskID,
		DataB64: base64.StdEncoding.EncodeToString(data),
		Payload: webproto.MustJSON(payload),
	})
}

func handleFileWrite(msg webproto.Message, send func(webproto.Message)) {
	var payload webproto.FileRPCPayload
	if len(msg.Payload) > 0 {
		_ = json.Unmarshal(msg.Payload, &payload)
	}
	payload.Path = strings.TrimSpace(payload.Path)
	if payload.Path == "" {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: "file path required"})
		return
	}

	data, err := base64.StdEncoding.DecodeString(msg.DataB64)
	if err != nil {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: "decode file: " + err.Error()})
		return
	}
	if err := os.MkdirAll(filepath.Dir(payload.Path), 0o755); err != nil {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: err.Error()})
		return
	}
	if err := os.WriteFile(payload.Path, data, 0o644); err != nil {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: err.Error()})
		return
	}
	payload.Size = int64(len(data))
	send(webproto.Message{Type: "complete", TaskID: msg.TaskID, Payload: webproto.MustJSON(payload)})
}

func isREPLCommand(prompt string) bool {
	return strings.HasPrefix(prompt, "/") || strings.HasPrefix(prompt, "!")
}

func runChatREPLLine(ctx context.Context, line string, rt *runner.AgentRuntime, ag *agent.Agent) (string, error) {
	var stdout bytes.Buffer
	var stderr bytes.Buffer
	option := rt.Option
	if option != nil {
		copy := *option
		copy.NoColor = true
		option = &copy
	}
	appInfo := tui.AppInfo{
		Provider:          rt.App.Provider,
		ProviderConfig:    rt.App.ProviderConfig,
		ProviderFallbacks: rt.App.ProviderFallbacks,
		Commands:          rt.App.Commands,
		Skills:            rt.App.Skills,
		OnProviderChange: func(provider agent.Provider, providerConfig agent.ProviderConfig) {
			rt.App.Provider = provider
			rt.App.ProviderConfig = providerConfig
			rt.Config.Provider = provider
			rt.Config.Model = providerConfig.Model
		},
	}
	console := tui.NewAgentConsoleWithWriters(ctx, option, appInfo, ag, &stdout, &stderr, rt.Bus)
	_, err := console.ExecuteLineAndWait(line)
	out := trimChatOutput(output.StripANSI(stdout.String()))
	errOut := trimChatOutput(output.StripANSI(stderr.String()))
	if err != nil {
		if errOut != "" {
			return "", fmt.Errorf("%s: %w", errOut, err)
		}
		return "", err
	}
	combined := out
	switch {
	case out == "":
		combined = errOut
	case errOut != "":
		combined = trimChatOutput(out + "\n" + errOut)
	}
	return fenceTerminalOutput(combined), nil
}

// fenceTerminalOutput wraps multi-line REPL/`!` command output in a Markdown
// code fence. runChatREPLLine runs the same TUI console the interactive REPL
// uses, whose panels (/status, /provider, /nodes …) are drawn with box-drawing
// characters and column padding that only line up in a fixed-width,
// newline-preserving context. The web chat renders replies as Markdown prose,
// which collapses single newlines to spaces and uses a proportional font — so an
// unfenced panel flattens into one mangled line. A fence makes the frontend
// render it verbatim in a monospace <pre>. Single-line output (short status
// confirmations like "Provider ready: …") is left as prose.
func fenceTerminalOutput(s string) string {
	if !strings.Contains(s, "\n") {
		return s
	}
	// Opening fence must be longer than any backtick run inside the payload
	// (a `!cat` of a Markdown file could contain ```); grow it until it can't
	// collide. Panel output never contains backticks, so this is just insurance.
	fence := "```"
	for strings.Contains(s, fence) {
		fence += "`"
	}
	return fence + "\n" + s + "\n" + fence
}

func trimChatOutput(value string) string {
	return strings.TrimRight(value, " \t\r\n")
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
		Name:         name,
		Commands:     reg.Names(),
		CommandsMenu: agentCommandCatalog(rt),
		Stats:        stats,
		Identity:     agentIdentity(rt),
	}
	if payload.Identity.NodeName == "" {
		payload.Identity.NodeName = name
	}
	return payload
}

// agentCommandCatalog is the agent's user-facing "/verb" catalog reported to the
// hub on register: the static agent-scope menu commands plus one per loaded (and
// non-internal) skill. The hub merges it with its hub-scope commands to build
// the web "/" menu and /help, so the menu reflects what this agent can run.
func agentCommandCatalog(rt *runner.AgentRuntime) []webproto.CommandSpec {
	// Build a zero-value console to extract command metadata without a live session.
	r := &tui.AgentConsole{}
	specs := tui.WebMenuSpecs(r.StaticCommands())
	if rt == nil || rt.App == nil || rt.App.Skills == nil {
		return specs
	}
	for _, sk := range rt.App.Skills.Skills {
		if strings.TrimSpace(sk.Name) == "" || sk.Internal {
			continue
		}
		specs = append(specs, webproto.CommandSpec{
			Name:        "/" + strings.TrimPrefix(strings.TrimSpace(sk.Name), "/"),
			Description: sk.Description,
		})
	}
	return specs
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

// splitAccessKey lifts the access token out of a URL's userinfo
// (http://<token>@host…), returning a userinfo-free URL plus the token. A URL
// without userinfo (or an unparseable one) comes back unchanged with an empty token.
func splitAccessKey(rawURL string) (dialURL, token string) {
	u, err := url.Parse(rawURL)
	if err != nil || u.User == nil {
		return rawURL, ""
	}
	token = u.User.Username()
	u.User = nil
	return u.String(), token
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
