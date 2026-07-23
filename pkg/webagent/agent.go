package webagent

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/runner"
	"github.com/chainreactors/aiscan/pkg/agent"
	inboxpkg "github.com/chainreactors/aiscan/pkg/agent/inbox"
	"github.com/chainreactors/aiscan/pkg/aop"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/aiscan/pkg/tui"
	"github.com/chainreactors/aiscan/pkg/webproto"
	"github.com/chainreactors/ioa/protocols"
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

	rt, err := runner.NewAgentRuntime(ctx, option, logger, &runner.RuntimeConfig{
		NoOutput:         true,
		IOA:              remoteIOAConfig(option, identityRef),
		ProviderOptional: true,
		REPLMode:         runner.REPLPersistent,
	})
	if err != nil {
		return err
	}
	defer rt.Close()

	chatHandler := &chatAgentHandler{
		rt:        rt,
		serverURL: option.WebURL,
	}

	connectionDone := make(chan struct{})
	go func() {
		defer close(connectionDone)
		_ = rt.App.WaitEngines(ctx)
		logger.Debugf("websocket transport connection to %s", option.WebURL)

		_ = connect(ctx, connectionConfig{
			ServerURL: option.WebURL,
			Name:      rt.NodeName,
			Registry:  rt.App.Commands,
			AgentBus:  rt.Bus,
			DataBus:   rt.App.DataBus,
			SCO:       rt.App.SCOSidecar,
			Logger:    logger,
			Chat:      chatHandler,
			Node:      identityRef,
			Runtime:   DefaultRuntime(),
			Status:    func() webproto.AgentStatus { return agentStatus(rt) },
			Menu:      func() []webproto.CommandSpec { return agentCommandCatalog(rt) },
			PTYRouter: rt.NewPTYRouter,
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
		logger.Infof("websocket transport connected; remote REPL and PTY are available")
		<-ctx.Done()
		<-connectionDone
		return nil
	}

	_, err = rt.Execute(ctx, "startup", agent.Inbound{
		Kind:  agent.InboundUserMessage,
		Event: aop.Event{SessionID: "startup"},
		Message: aop.MessageData{
			MessageID: "startup",
			Role:      "user",
			Parts:     []aop.MessagePart{{Type: aop.PartText, Text: task}},
		},
	}, nil)

	<-connectionDone
	return err
}

// ---------------------------------------------------------------------------
// chatAgentHandler implements the private connection chat handler.
// ---------------------------------------------------------------------------

type chatAgentHandler struct {
	rt        *runner.AgentRuntime
	serverURL string
}

func (h *chatAgentHandler) HandleChat(ctx context.Context, msg webproto.Message, event aop.Event, send func(webproto.Message), router *eventRouter) func() {
	inbound, err := agent.Classify(event)
	if err != nil || inbound.Kind != agent.InboundUserMessage {
		router.Route(replSessionID(msg.TaskID), msg.TaskID)
		return func() {
			h.emitREPLSession(msg.TaskID, "", fmt.Errorf("invalid inbound user message"))
		}
	}
	prompt := strings.TrimSpace(webproto.UserMessageText(event))
	if prompt == "" {
		router.Route(replSessionID(msg.TaskID), msg.TaskID)
		return func() {
			h.emitREPLSession(msg.TaskID, "", fmt.Errorf("empty prompt"))
		}
	}

	if isREPLCommand(prompt) {
		router.Route(replSessionID(msg.TaskID), msg.TaskID)
		wait, submitErr := h.rt.SubmitLine(ctx, msg.TaskID, event.SessionID, prompt)
		return func() {
			if submitErr != nil {
				h.emitREPLSession(msg.TaskID, "", submitErr)
				return
			}
			out, execErr := wait()
			h.emitREPLSession(msg.TaskID, out, execErr)
		}
	}

	wait, err := h.rt.Submit(ctx, msg.TaskID, inbound, func(sessionID string) {
		router.Route(sessionID, msg.TaskID)
	})
	if err != nil {
		router.Route(replSessionID(msg.TaskID), msg.TaskID)
		return func() {
			h.emitREPLSession(msg.TaskID, "", err)
		}
	}
	return func() {
		// Run failures are terminal on the root session.end emitted by the
		// agent kernel; nothing to send here.
		_, _ = wait()
	}
}

// replSessionID names the self-contained AOP session that reports one
// REPL/slash-command run. It has no parent session so the hub converges the
// chat task on its session.end.
func replSessionID(taskID string) string {
	return "repl-" + taskID
}

// emitREPLSession publishes a REPL command run as a three-event AOP session on
// the runtime bus: session.start, one assistant message with the fenced
// output (skipped on error), and session.end carrying the stop reason.
func (h *chatAgentHandler) emitREPLSession(taskID, out string, runErr error) {
	sessionID := replSessionID(taskID)
	seq := 0
	emit := func(typ string, data any) {
		raw, err := json.Marshal(data)
		if err != nil {
			return
		}
		seq++
		h.rt.Bus.Emit(aop.Event{
			Type:      typ,
			TS:        time.Now().UTC().Format(time.RFC3339Nano),
			SessionID: sessionID,
			Agent:     h.rt.NodeName,
			Seq:       seq,
			Data:      raw,
		})
	}
	emit(aop.TypeSessionStart, aop.SessionStartData{})
	if runErr == nil && out != "" {
		emit(aop.TypeMessage, aop.MessageData{
			MessageID: sessionID + "-output",
			Role:      "assistant",
			Parts:     []aop.MessagePart{{Type: aop.PartText, Text: fenceTerminalOutput(out)}},
		})
	}
	end := aop.SessionEndData{Stop: string(agent.StopReasonCompleted)}
	if runErr != nil {
		end.Stop = string(agent.StopReasonError)
		if errors.Is(runErr, context.Canceled) {
			end.Stop = string(agent.StopReasonCanceled)
		}
		end.Error = runErr.Error()
	}
	emit(aop.TypeSessionEnd, end)
}

func (h *chatAgentHandler) HandleUpload(msg webproto.Message, send func(webproto.Message)) {
	handleFileUpload(msg, send, h.rt)
}

func (h *chatAgentHandler) HandleConfigReload(serverURL string, send func(webproto.Message)) {
	provider, model, err := reloadAgentConfig(serverURL, h.rt)
	result := webproto.ConfigReloadResult{OK: err == nil, Model: model}
	if err != nil {
		result.Error = err.Error()
	} else {
		result.Provider = provider.Name()
		statusPayload, _ := json.Marshal(agentStatus(h.rt))
		send(webproto.Message{Type: "agent.status", Payload: statusPayload})
	}
	resultPayload, _ := json.Marshal(result)
	send(webproto.Message{Type: "config.result", Payload: resultPayload})
}

func (h *chatAgentHandler) CancelChat(taskID string) bool {
	return h.rt.Cancel(taskID)
}

// ---------------------------------------------------------------------------
// reloadAgentConfig re-fetches the hub config and hot-swaps the LLM provider so
// a running agent picks up a Settings change without a restart. Best-effort: a
// fetch/build failure leaves the current provider in place. serverURL is the hub
// base the agent already dials. Returns the live provider, resolved model, and
// true when the swap succeeded, so the caller can re-announce identity.
// ---------------------------------------------------------------------------

func reloadAgentConfig(serverURL string, rt *runner.AgentRuntime) (agent.Provider, string, error) {
	if rt == nil {
		return nil, "", fmt.Errorf("agent runtime is not configured")
	}
	logger := rt.Config.Logger
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	remoteOpt, err := fetchRemoteConfig(serverURL)
	if err != nil {
		logger.Warnf("config reload: fetch remote config: %s", err)
		return nil, "", err
	}
	provider, model, err := rt.ReloadProvider(remoteOpt)
	if err != nil {
		logger.Warnf("config reload: rebuild provider: %s", err)
		return nil, "", err
	}
	logger.Importantf("config reloaded: provider=%s model=%s", provider.Name(), model)
	return provider, model, nil
}

// ---------------------------------------------------------------------------
// File upload
// ---------------------------------------------------------------------------

func handleFileUpload(msg webproto.Message, send func(webproto.Message), rt *runner.AgentRuntime) {
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

	// Surface the path through the target session's Inbox. Slash/direct commands
	// do not consume the Inbox, so the note remains available for the next agent
	// turn without a Web-specific pending-upload map.
	note := fmt.Sprintf(
		"[已上传文件] 名称=%q 大小=%d 字节 · agent 本地绝对路径: %s\n（该文件已保存在 agent 磁盘上，需要查看内容时用 read 工具打开上述绝对路径。）",
		payload.Filename, len(data), dest)
	if err := rt.PushInbox(payload.SessionID, inboxpkg.NewMessage(inboxpkg.OriginSystem, "user", note)); err != nil {
		send(webproto.Message{
			Type:    "complete",
			TaskID:  msg.TaskID,
			Payload: webproto.MustJSON(webproto.FileUploadResult{Filename: payload.Filename, Error: err.Error()}),
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

func agentStatus(rt *runner.AgentRuntime) webproto.AgentStatus {
	var status webproto.AgentStatus
	if rt == nil {
		return status
	}
	if rt.Option != nil {
		status.Space = rt.Option.Space
	}
	if rt.App != nil {
		status.Provider = rt.App.ProviderConfig.Provider
		status.Model = rt.App.ProviderConfig.Model
		status.Bound = ioaBound(rt)
	}
	return status
}

func ioaBound(rt *runner.AgentRuntime) bool {
	if rt == nil || rt.App == nil || rt.App.IOAClient == nil {
		return false
	}
	return rt.App.IOAClient.Bound()
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
