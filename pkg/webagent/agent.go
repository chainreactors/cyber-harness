package webagent

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/node"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/core/runner"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/agent/evaluator"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tui"
	"github.com/chainreactors/aiscan/pkg/webproto"
	"github.com/chainreactors/utils/pty"
)

func Run(ctx context.Context, option *cfg.Option, logger telemetry.Logger) error {
	if option.WebURL != "" {
		remoteOpt, err := fetchRemoteConfig(option.WebURL)
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

	chatHandler := &chatAgentHandler{
		rt:        rt,
		chatMgr:   newChatRuntimeManager(rt),
		serverURL: option.WebURL,
	}

	connectionDone := make(chan struct{})
	go func() {
		defer close(connectionDone)
		_ = rt.App.WaitEngines(ctx)
		logger.Debugf("web agent connection to %s", option.WebURL)

		var extraPTYOpeners map[string]pty.OpenFunc
		if mgr := node.RegistryPTYManager(rt.App.Commands); mgr != nil {
			extraPTYOpeners = map[string]pty.OpenFunc{
				"repl": runner.NewRemoteREPLOpener(rt, mgr),
			}
		}

		_ = node.Connect(ctx, node.ConnectConfig{
			ServerURL:       option.WebURL,
			Name:            rt.NodeName,
			Registry:        rt.App.Commands,
			AgentBus:        rt.Bus,
			DataBus:         rt.App.DataBus,
			SCO:             rt.App.SCOSidecar,
			Logger:          logger,
			Chat:            chatHandler,
			Identity:        func() webproto.AgentIdentity { return agentIdentity(rt) },
			Menu:            func() []webproto.CommandSpec { return agentCommandCatalog(rt) },
			ExtraPTYOpeners: extraPTYOpeners,
		})
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

// ---------------------------------------------------------------------------
// chatAgentHandler implements node.ChatHandler
// ---------------------------------------------------------------------------

type chatAgentHandler struct {
	rt        *runner.AgentRuntime
	chatMgr   *chatRuntimeManager
	serverURL string
}

func (h *chatAgentHandler) HandleChat(ctx context.Context, msg webproto.Message, send func(webproto.Message), router *node.EventRouter) {
	chatOpts, err := parseChatPayload(msg)
	if err != nil {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: err.Error()})
		return
	}
	webSessionID := chatOpts.SessionID
	ag, agErr := h.chatMgr.agentFor(webSessionID)
	if agErr != nil {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: agErr.Error()})
		return
	}

	prompt := strings.TrimSpace(msg.Data)
	if prompt == "" {
		send(webproto.Message{Type: "error", TaskID: msg.TaskID, Data: "empty prompt"})
		return
	}

	// Always route future events to the latest message.
	router.Route(ag.Cfg.SessionID, msg.TaskID)

	if ag.IsRunning() {
		// Agent is busy -- append to inbox; the loop picks it up. Leave any
		// pending upload notes queued so they ride the next idle turn rather
		// than being drained into a steer that may not surface them.
		ag.SteerUserMessage(prompt)
		send(webproto.Message{Type: "complete", TaskID: msg.TaskID})
		return
	}

	// Idle turn: fold in files uploaded to this session since the last turn so
	// the agent learns their absolute on-disk paths and can read them. REPL/`!`
	// lines are left untouched so a note never corrupts a command; the note
	// stays queued for the next natural-language turn.
	if !isREPLCommand(prompt) {
		if note := h.chatMgr.takePendingUploads(webSessionID); note != "" {
			msg.Data = note + "\n\n" + prompt
		}
	}

	// Agent is idle -- start a new run with this message. The node already
	// launched us in a goroutine with a cancellable context, so we run
	// synchronously here.
	runChatWithAgent(ctx, msg, chatOpts, ag, h.rt, send)
}

func (h *chatAgentHandler) HandleUpload(msg webproto.Message, send func(webproto.Message)) {
	handleFileUpload(msg, send, h.chatMgr)
}

func (h *chatAgentHandler) HandleConfigReload(serverURL string, send func(webproto.Message)) {
	if provider, model, ok := reloadAgentConfig(serverURL, h.rt, h.chatMgr); ok {
		payload, _ := json.Marshal(webproto.AgentIdentity{Provider: provider.Name(), Model: model})
		send(webproto.Message{Type: "agent.identity", Payload: payload})
	}
}

func (h *chatAgentHandler) CancelChat(taskID string) bool {
	return false // cancel is handled by node's context cancellation
}

// ---------------------------------------------------------------------------
// parseChatPayload decodes the "chat" WS payload: the web session to scope the
// agent conversation to, plus optional Goal-mode run controls.
// ---------------------------------------------------------------------------

func parseChatPayload(msg webproto.Message) (webproto.ChatPayload, error) {
	return webproto.DecodeChatPayload(msg.Payload)
}

// ---------------------------------------------------------------------------
// chatRuntimeManager
// ---------------------------------------------------------------------------

type chatRuntimeManager struct {
	rt       *runner.AgentRuntime
	mu       sync.Mutex
	sessions map[string]*agent.Agent

	uploadMu sync.Mutex
	uploads  map[string][]string // web sessionID -> notes about files uploaded since the last turn
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

// ---------------------------------------------------------------------------
// reloadAgentConfig re-fetches the hub config and hot-swaps the LLM provider so
// a running agent picks up a Settings change without a restart. Best-effort: a
// fetch/build failure leaves the current provider in place. serverURL is the hub
// base the agent already dials. Returns the live provider, resolved model, and
// true when the swap succeeded, so the caller can re-announce identity.
// ---------------------------------------------------------------------------

func reloadAgentConfig(serverURL string, rt *runner.AgentRuntime, cr *chatRuntimeManager) (agent.Provider, string, bool) {
	if rt == nil {
		return nil, "", false
	}
	logger := rt.Config.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	remoteOpt, err := fetchRemoteConfig(serverURL)
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

// ---------------------------------------------------------------------------
// Chat execution
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// File upload
// ---------------------------------------------------------------------------

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

// ---------------------------------------------------------------------------
// REPL helpers
// ---------------------------------------------------------------------------

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
// uses, whose panels (/status, /provider, /nodes ...) are drawn with box-drawing
// characters and column padding that only line up in a fixed-width,
// newline-preserving context. The web chat renders replies as Markdown prose,
// which collapses single newlines to spaces and uses a proportional font -- so an
// unfenced panel flattens into one mangled line. A fence makes the frontend
// render it verbatim in a monospace <pre>. Single-line output (short status
// confirmations like "Provider ready: ...") is left as prose.
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

// ---------------------------------------------------------------------------
// Identity and command catalog (agent-specific, needs runner.AgentRuntime)
// ---------------------------------------------------------------------------

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
	identity := node.DefaultIdentity()
	if rt == nil {
		return identity
	}
	identity.NodeName = rt.NodeName
	if rt.Option != nil {
		identity.Space = rt.Option.Space
		identity.IOAURL = node.PublicIOAURL(rt.Option.IOAURL)
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

// ---------------------------------------------------------------------------
// Startup helpers
// ---------------------------------------------------------------------------

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
