package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
	transport "github.com/chainreactors/aiscan/aop/aiscan/transport"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/runner"
	"github.com/chainreactors/ioa/protocols"
	"github.com/chainreactors/utils/pty"
)

func RunWebSocket(ctx context.Context, option *cfg.Option, logger telemetry.Logger) error {
	return runRemoteAgent(ctx, option, logger, false)
}

func RunGRPC(ctx context.Context, option *cfg.Option, logger telemetry.Logger) error {
	return runRemoteAgent(ctx, option, logger, true)
}

func runRemoteAgent(ctx context.Context, option *cfg.Option, logger telemetry.Logger, grpcTransport bool) error {
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

	appConfig := runner.AppConfig(option, runner.RuntimeFeatures{
		ProviderEnabled: true, ProviderOptional: true, ToolsEnabled: true, AIEnabled: true,
	}, logger)
	appConfig.IOA = remoteIOAConfig(option, identityRef)
	application, err := runner.NewApp(ctx, appConfig)
	if err != nil {
		return err
	}
	defer application.Close()
	runner.ApplyResolvedProviderOptions(option, application.ProviderConfig)
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
		app:       application,
		option:    option,
		logger:    logger,
	}

	connectionDone := make(chan struct{})
	go func() {
		defer close(connectionDone)
		_ = application.WaitEngines(ctx)
		transportName := "websocket"
		if grpcTransport {
			transportName = "grpc"
		}
		logger.Debugf("%s transport connection to %s", transportName, option.WebURL)

		connection := connectionConfig{
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
			Status:         func() *transport.AgentStatus { return agentStatus(option, application) },
			Menu:           func() []*transport.CommandSpec { return agentCommandCatalog(application) },
			PTYRouter:      func() (*pty.Router, error) { return NewPTYRouter(application.Commands), nil },
		}
		if grpcTransport {
			_ = connectGenerated(ctx, connection, true)
		} else {
			_ = connect(ctx, connection)
		}
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
		logger.Infof("remote transport connected; remote REPL and PTY are available")
		<-ctx.Done()
		<-connectionDone
		return nil
	}

	_, err = rt.EnsureSession(runner.SessionOptions{ID: "startup"})
	if err != nil {
		return err
	}
	run, err := rt.RunSession(ctx, "startup", runner.RunInput{TurnID: "startup", Content: []*aop.Content{aop.Text(task)}})
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
	app       *runner.App
	option    *cfg.Option
	logger    telemetry.Logger
}

func (h *chatAgentHandler) OpenSession(ctx context.Context, req *aop.OpenSessionRequest) *aop.OpenSessionResponse {
	return h.rt.OpenAOPSession(req)
}

func (h *chatAgentHandler) RunTurn(ctx context.Context, req *aop.RunTurnRequest) *aop.RunTurnResponse {
	return h.rt.RunAOPTurn(ctx, req)
}

func (h *chatAgentHandler) CancelTurn(req *aop.CancelTurnRequest) *aop.CancelTurnResponse {
	return h.rt.CancelAOPTurn(req)
}

func (h *chatAgentHandler) CloseSession(ctx context.Context, req *aop.CloseSessionRequest) *aop.CloseSessionResponse {
	return h.rt.CloseAOPSession(ctx, req)
}

func (h *chatAgentHandler) Command(ctx context.Context, req *transport.CommandRequest) (*transport.CommandResult, error) {
	if h.rt == nil || req == nil || strings.TrimSpace(req.Line) == "" {
		return nil, fmt.Errorf("command line is required")
	}
	result, err := h.rt.CommandSession(ctx, req.SessionId, req.Line)
	if err != nil {
		return nil, err
	}
	encoded, err := json.Marshal(result)
	if err != nil {
		return nil, err
	}
	return &transport.CommandResult{TaskId: req.TaskId, Result: encoded, MediaType: "application/json"}, nil
}

func (h *chatAgentHandler) Upload(req *transport.FileUploadRequest) (*transport.FileResult, error) {
	if req == nil {
		return nil, fmt.Errorf("upload request is required")
	}
	filename := filepath.Base(strings.TrimSpace(req.Filename))
	if filename == "." || filename == "" {
		filename = "upload"
	}
	dir := filepath.Join(os.TempDir(), "aiscan-uploads")
	if err := os.MkdirAll(dir, 0o755); err != nil {
		return nil, err
	}
	dest := filepath.Join(dir, filename)
	if err := os.WriteFile(dest, req.Data, 0o644); err != nil {
		return nil, err
	}
	return &transport.FileResult{TaskId: req.TaskId, Filename: filename, Path: dest, Size: int64(len(req.Data))}, nil
}

func (h *chatAgentHandler) ReloadConfig(serverURL string) (*transport.ConfigReloadResult, *transport.AgentStatus) {
	provider, model, err := reloadAgentConfig(serverURL, h.rt, h.app, h.logger)
	result := &transport.ConfigReloadResult{Ok: err == nil, Model: model}
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	result.Provider = provider.Name()
	return result, agentStatus(h.option, h.app)
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
	providerConfig := runner.ProviderConfig(remoteOpt)
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
// Identity and command catalog (agent-specific, needs runner.AgentRuntime)
// ---------------------------------------------------------------------------

// agentCommandCatalog is the agent's user-facing "/verb" catalog reported to the
// hub on register: the static agent-scope menu commands plus one per loaded (and
// non-internal) skill. The hub merges it with its hub-scope commands to build
// the web "/" menu and /help, so the menu reflects what this agent can run.
func agentCommandCatalog(app *runner.App) []*transport.CommandSpec {
	specs := runner.RuntimeCommandSpecs()
	if app == nil || app.Skills == nil {
		return specs
	}
	for _, sk := range app.Skills.Skills {
		if strings.TrimSpace(sk.Name) == "" || sk.Internal {
			continue
		}
		specs = append(specs, &transport.CommandSpec{
			Name:        "/skill:" + strings.TrimPrefix(strings.TrimSpace(sk.Name), "/"),
			Description: sk.Description,
		})
	}
	return specs
}

func agentStatus(option *cfg.Option, app *runner.App) *transport.AgentStatus {
	status := new(transport.AgentStatus)
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

func remoteIOAConfig(option *cfg.Option, ref protocols.NodeRef) *runner.IOAConfig {
	if option == nil || option.IOAURL == "" {
		return nil
	}
	return &runner.IOAConfig{
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
