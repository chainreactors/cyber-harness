package webagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/runner"
	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tui"
	"github.com/chainreactors/aiscan/pkg/webproto"
	"github.com/chainreactors/ioa/protocols"
	"github.com/chainreactors/utils/pty"
)

func RunWebSocket(ctx context.Context, option *cfg.Option, logger telemetry.Logger) error {
	if option.WebURL != "" {
		remoteOpt, err := fetchRemoteConfig(option.WebURL)
		if err != nil {
			logger.Warnf("fetch remote config from %s: %s (continuing with local config)", option.WebURL, err)
		} else {
			logger.Infof("fetched remote config from %s", option.WebURL)
			cfg.MergeRemoteOption(option, remoteOpt)
		}
	}
	if strings.TrimSpace(option.IOAURL) == "" {
		return fmt.Errorf("ioa.url is required for web node identity")
	}
	identityRef, err := webNodeRef(option)
	if err != nil {
		return err
	}

	appConfig := cfg.AppConfig(option, cfg.RuntimeFeatures{
		ProviderEnabled: true, ProviderOptional: true, ToolsEnabled: true, AIEnabled: true,
	}, logger)
	appConfig.IOA = remoteIOAConfig(option, identityRef)
	application, err := runner.NewApp(ctx, appConfig)
	if err != nil {
		return err
	}
	defer application.Close()
	cfg.ApplyResolvedProviderOptions(option, application.ProviderConfig)
	rt, err := runner.NewAgentRuntime(ctx, option, logger, &runner.RuntimeConfig{
		ExistingApp: application, NoOutput: true, REPLMode: runner.REPLPersistent,
	})
	if err != nil {
		return err
	}
	defer rt.Close()

	chatHandler := &chatAgentHandler{
		rt:        rt,
		serverURL: option.WebURL,
		sessions:  make(map[string]*runner.Session),
		app:       application,
		option:    option,
		logger:    logger,
	}

	connectionDone := make(chan struct{})
	go func() {
		defer close(connectionDone)
		_ = application.WaitEngines(ctx)
		logger.Debugf("websocket transport connection to %s", option.WebURL)

		_ = connect(ctx, connectionConfig{
			ServerURL:      option.WebURL,
			Name:           runner.ResolveIOANodeName(option),
			Registry:       application.Commands,
			AgentSubscribe: rt.Subscribe,
			DataBus:        application.DataBus,
			SCO:            application.SCOSidecar,
			Logger:         logger,
			Chat:           chatHandler,
			Node:           identityRef,
			Runtime:        DefaultRuntime(),
			Status:         func() webproto.AgentStatus { return agentStatus(option, application) },
			Menu:           func() []webproto.CommandSpec { return agentCommandCatalog(application) },
			PTYRouter:      func() (*pty.Router, error) { return NewPTYRouter(application.Commands), nil },
		})
	}()

	if application.Provider == nil {
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
		logger.Infof("websocket transport connected; remote REPL and PTY are available")
		<-ctx.Done()
		<-connectionDone
		return nil
	}

	startup, err := rt.OpenSession(ctx, runner.SessionOptions{ID: "startup"})
	if err != nil {
		return err
	}
	chatHandler.sessions["startup"] = startup
	run, err := startup.Run(ctx, runner.RunInput{TurnID: "startup", Parts: []aop.MessagePart{{Type: aop.PartText, Text: task}}})
	if err == nil {
		_, err = run.Wait()
	}
	_ = rt.CloseSession(context.Background(), "startup", runner.SessionCloseCompleted)

	<-connectionDone
	return err
}

// ---------------------------------------------------------------------------
// chatAgentHandler implements the private connection chat handler.
// ---------------------------------------------------------------------------

type chatAgentHandler struct {
	rt        *runner.AgentRuntime
	serverURL string
	mu        sync.Mutex
	sessions  map[string]*runner.Session
	app       *runner.App
	option    *cfg.Option
	logger    telemetry.Logger
}

func (h *chatAgentHandler) HandleSessionOpen(ctx context.Context, msg webproto.Message, send func(webproto.Message)) {
	var payload webproto.SessionOpenPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		sendProtocolError(send, "", "", err)
		return
	}
	session, err := h.rt.OpenSession(ctx, runner.SessionOptions{
		ID: payload.SessionID, ParentSessionID: payload.ParentSessionID, ParentToolCallID: payload.ParentToolCallID,
	})
	if err != nil {
		sendProtocolError(send, "", "", err)
		return
	}
	sessionID := session.Snapshot().ID
	h.mu.Lock()
	h.sessions[sessionID] = session
	h.mu.Unlock()
	encoded, _ := json.Marshal(webproto.SessionLifecyclePayload{SessionID: sessionID})
	send(webproto.Message{Type: webproto.TypeSessionOpened, Payload: encoded})
}

func (h *chatAgentHandler) HandleSessionClose(ctx context.Context, msg webproto.Message, send func(webproto.Message)) {
	var payload webproto.SessionLifecyclePayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		sendProtocolError(send, "", "", err)
		return
	}
	reason := runner.SessionCloseReason(payload.Reason)
	if err := h.rt.CloseSession(ctx, payload.SessionID, reason); err != nil {
		sendProtocolError(send, "", "", err)
		return
	}
	h.mu.Lock()
	delete(h.sessions, payload.SessionID)
	h.mu.Unlock()
	encoded, _ := json.Marshal(webproto.SessionLifecyclePayload{SessionID: payload.SessionID, Reason: payload.Reason})
	send(webproto.Message{Type: webproto.TypeSessionClosed, Payload: encoded})
}

func (h *chatAgentHandler) HandleRun(ctx context.Context, msg webproto.Message, send func(webproto.Message)) func() {
	var payload webproto.RunPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		return func() { sendProtocolError(send, msg.TurnID, "", err) }
	}
	h.mu.Lock()
	session := h.sessions[payload.SessionID]
	h.mu.Unlock()
	if session == nil {
		return func() {
			sendProtocolError(send, msg.TurnID, "", fmt.Errorf("session %q is not open", payload.SessionID))
		}
	}
	input := runner.RunInput{
		TurnID: msg.TurnID, Parts: payload.Parts, NoEcho: payload.NoEcho, MaxTurns: payload.MaxTurns,
		EvalCriteria: payload.EvalCriteria, EvalMaxRounds: payload.EvalMaxRounds,
	}
	prompt := strings.TrimSpace(partsText(payload.Parts))
	if prompt == "/continue" {
		input.Continue = true
		input.Parts = nil
	} else if strings.HasPrefix(prompt, "/followup ") {
		input.Parts = []aop.MessagePart{{Type: aop.PartText, Text: strings.TrimSpace(strings.TrimPrefix(prompt, "/followup "))}}
	}
	run, err := session.Run(ctx, input)
	if err != nil {
		return func() { sendProtocolError(send, msg.TurnID, "", err) }
	}
	return func() { _, _ = run.Wait() }
}

func (h *chatAgentHandler) HandleCommand(ctx context.Context, msg webproto.Message, send func(webproto.Message)) {
	var payload webproto.CommandPayload
	if err := json.Unmarshal(msg.Payload, &payload); err != nil {
		sendProtocolError(send, "", msg.TaskID, err)
		return
	}
	h.mu.Lock()
	session := h.sessions[payload.SessionID]
	h.mu.Unlock()
	if session == nil {
		sendProtocolError(send, "", msg.TaskID, fmt.Errorf("session %q is not open", payload.SessionID))
		return
	}
	result, err := session.Command(ctx, runner.CommandInput{Line: payload.Line})
	if err != nil {
		sendProtocolError(send, "", msg.TaskID, err)
		return
	}
	for i := range result.Parts {
		if result.Parts[i].Type == aop.PartText {
			result.Parts[i].Text = fenceTerminalOutput(result.Parts[i].Text)
		}
	}
	encoded, _ := json.Marshal(webproto.CommandResultPayload{SessionID: payload.SessionID, Parts: result.Parts, Metadata: result.Metadata})
	send(webproto.Message{Type: webproto.TypeCommandResult, TaskID: msg.TaskID, Payload: encoded})
}

func sendProtocolError(send func(webproto.Message), turnID, taskID string, err error) {
	payload, _ := json.Marshal(webproto.ErrorPayload{Message: err.Error()})
	send(webproto.Message{Type: webproto.TypeError, TurnID: turnID, TaskID: taskID, Payload: payload})
}

func partsText(parts []aop.MessagePart) string {
	var values []string
	for _, part := range parts {
		if part.Type == aop.PartText && part.Text != "" {
			values = append(values, part.Text)
		}
	}
	return strings.Join(values, "\n")
}

func (h *chatAgentHandler) HandleUpload(msg webproto.Message, send func(webproto.Message)) {
	handleFileUpload(msg, send)
}

func (h *chatAgentHandler) HandleConfigReload(serverURL string, send func(webproto.Message)) {
	provider, model, err := reloadAgentConfig(serverURL, h.rt, h.app, h.logger)
	result := webproto.ConfigReloadResult{OK: err == nil, Model: model}
	if err != nil {
		result.Error = err.Error()
	} else {
		result.Provider = provider.Name()
		statusPayload, _ := json.Marshal(agentStatus(h.option, h.app))
		send(webproto.Message{Type: "agent.status", Payload: statusPayload})
	}
	resultPayload, _ := json.Marshal(result)
	send(webproto.Message{Type: "config.result", Payload: resultPayload})
}

// ---------------------------------------------------------------------------
// reloadAgentConfig re-fetches the hub config and hot-swaps the LLM provider so
// a running agent picks up a Settings change without a restart. Best-effort: a
// fetch/build failure leaves the current provider in place. serverURL is the hub
// base the agent already dials. Returns the live provider, resolved model, and
// true when the swap succeeded, so the caller can re-announce identity.
// ---------------------------------------------------------------------------

func reloadAgentConfig(serverURL string, rt *runner.AgentRuntime, app *runner.App, logger telemetry.Logger) (agent.Provider, string, error) {
	if rt == nil {
		return nil, "", fmt.Errorf("agent runtime is not configured")
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	remoteOpt, err := fetchRemoteConfig(serverURL)
	if err != nil {
		logger.Warnf("config reload: fetch remote config: %s", err)
		return nil, "", err
	}
	providerConfig := cfg.ProviderConfig(remoteOpt)
	resolved, err := agent.ResolveProvider(&providerConfig)
	if err != nil {
		logger.Warnf("config reload: resolve provider: %s", err)
		return nil, "", err
	}
	provider, err := agent.NewProviderFromResolved(resolved)
	if err != nil {
		logger.Warnf("config reload: rebuild provider: %s", err)
		return nil, "", err
	}
	model := resolved.Model
	app.Provider = provider
	app.ProviderConfig = *resolved
	rt.SetProvider(provider, *resolved)
	logger.Importantf("config reloaded: provider=%s model=%s", provider.Name(), model)
	return provider, model, nil
}

// ---------------------------------------------------------------------------
// File upload
// ---------------------------------------------------------------------------

func handleFileUpload(msg webproto.Message, send func(webproto.Message)) {
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

	send(webproto.Message{
		Type:   "complete",
		TaskID: msg.TaskID,
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

// ---------------------------------------------------------------------------
// Identity and command catalog (agent-specific, needs runner.AgentRuntime)
// ---------------------------------------------------------------------------

// agentCommandCatalog is the agent's user-facing "/verb" catalog reported to the
// hub on register: the static agent-scope menu commands plus one per loaded (and
// non-internal) skill. The hub merges it with its hub-scope commands to build
// the web "/" menu and /help, so the menu reflects what this agent can run.
func agentCommandCatalog(app *runner.App) []webproto.CommandSpec {
	// Build a zero-value console to extract command metadata without a live session.
	r := &tui.AgentConsole{}
	specs := tui.WebMenuSpecs(r.StaticCommands())
	if app == nil || app.Skills == nil {
		return specs
	}
	for _, sk := range app.Skills.Skills {
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

func agentStatus(option *cfg.Option, app *runner.App) webproto.AgentStatus {
	var status webproto.AgentStatus
	if option != nil {
		status.Space = option.Space
	}
	if app != nil {
		status.Provider = app.ProviderConfig.Provider
		status.Model = app.ProviderConfig.Model
		status.Bound = ioaBound(app)
	}
	return status
}

func ioaBound(app *runner.App) bool {
	if app == nil || app.IOAClient == nil {
		return false
	}
	return app.IOAClient.Bound()
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

type webIdentity struct{ ref protocols.NodeRef }

func (i webIdentity) IOABinding() protocols.IdentityBinding {
	return protocols.IdentityBinding{
		Namespace: "aiscan.web",
		Subject:   i.ref.URI(),
	}
}

func webNodeRef(option *cfg.Option) (protocols.NodeRef, error) {
	if option == nil {
		return protocols.NodeRef{}, fmt.Errorf("web node configuration is required")
	}
	authority, err := protocols.CanonicalAuthority(option.WebURL)
	if err != nil {
		return protocols.NodeRef{}, fmt.Errorf("web node authority: %w", err)
	}
	name := strings.TrimSpace(option.IOANodeName)
	if name == "" {
		return protocols.NodeRef{}, fmt.Errorf("ioa.node_name is required for web node identity")
	}
	return protocols.NodeRef{ID: name, Authority: authority}, nil
}

func remoteIOAConfig(option *cfg.Option, ref protocols.NodeRef) *cfg.IOAConfig {
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
		NodeMeta:      map[string]any{"client": "aiscan", "transport": "websocket"},
		Identity:      webIdentity{ref: ref},
	}
}
