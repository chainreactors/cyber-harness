package agent

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
	filepb "github.com/chainreactors/aiscan/aop/file"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/runner"
	commandpb "github.com/chainreactors/aiscan/pkg/types/command"
	configpb "github.com/chainreactors/aiscan/pkg/types/config"
	reloadpb "github.com/chainreactors/aiscan/pkg/types/reload"
	"github.com/chainreactors/ioa/protocols"
	"github.com/chainreactors/utils/pty"
)

func RunWebSocket(ctx context.Context, option *cfg.Option, logger telemetry.Logger) error {
	return runRemoteAgent(ctx, option, logger)
}

func runRemoteAgent(ctx context.Context, option *cfg.Option, logger telemetry.Logger) error {
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
		rt:     rt,
		app:    application,
		option: option,
		logger: logger,
		ready:  make(chan struct{}),
	}

	connectionDone := make(chan struct{})
	go func() {
		defer close(connectionDone)
		_ = application.WaitEngines(ctx)
		dialURL, _ := SplitAccessKey(option.ServerURL)
		logger.Debugf("websocket transport connection to %s", dialURL)

		connection := connectionConfig{
			ServerURL:      option.ServerURL,
			Name:           runner.ResolveIOANodeName(option),
			Registry:       application.Commands,
			AgentSubscribe: rt.Subscribe,
			DataBus:        application.DataBus,
			SCO:            application.SCOSidecar,
			Logger:         logger,
			Chat:           chatHandler,
			Node:           identityRef,
			Runtime:        DefaultRuntime(),
			Status:         func() *aop.AgentStatus { return agentStatus(option, application) },
			Menu:           func() []*commandpb.Spec { return agentCommandCatalog(application) },
			PTYRouter:      func() (*pty.Router, error) { return NewPTYRouter(application.Commands), nil },
		}
		_ = connect(ctx, connection)
	}()

	if application.Provider == nil {
		select {
		case <-chatHandler.ready:
		case <-ctx.Done():
			<-connectionDone
			return nil
		}
	}
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
	app       *runner.App
	option    *cfg.Option
	logger    telemetry.Logger
	ready     chan struct{}
	readyOnce sync.Once
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

func (h *chatAgentHandler) Command(ctx context.Context, req *commandpb.Request) (*commandpb.Result, error) {
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
	return &commandpb.Result{Data: encoded, MediaType: "application/json"}, nil
}

func (h *chatAgentHandler) Upload(req *filepb.UploadRequest) (*filepb.Result, error) {
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
	return &filepb.Result{Filename: filename, Path: dest, Size: int64(len(req.Data))}, nil
}

func (h *chatAgentHandler) ReloadConfig(config *configpb.DistributeConfig) (*reloadpb.Result, *aop.AgentStatus) {
	defer h.readyOnce.Do(func() {
		if h.ready != nil {
			close(h.ready)
		}
	})
	provider, model, err := reloadAgentConfig(config, h.rt, h.app, h.option, h.logger)
	result := &reloadpb.Result{Ok: err == nil, Model: model}
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	result.Provider = provider.Name()
	return result, agentStatus(h.option, h.app)
}

// ---------------------------------------------------------------------------
// reloadAgentConfig hot-swaps the LLM provider from the protobuf config carried
// by the application WebSocket. A build failure leaves the current provider in
// place and is reported through the reload result and AgentStatus.
// ---------------------------------------------------------------------------

func reloadAgentConfig(distribute *configpb.DistributeConfig, rt *runner.AgentRuntime, app *runner.App, option *cfg.Option, logger telemetry.Logger) (agent.Provider, string, error) {
	if rt == nil {
		return nil, "", fmt.Errorf("agent runtime is not configured")
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	if distribute == nil {
		return nil, "", fmt.Errorf("remote config is required")
	}
	providerConfig := runner.ProviderConfigFromProto(distribute.GetLlm())
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
	if option != nil {
		runner.ApplyResolvedProviderOptions(option, *resolved)
	}
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
func agentCommandCatalog(app *runner.App) []*commandpb.Spec {
	specs := runner.RuntimeCommandSpecs()
	if app == nil || app.Skills == nil {
		return specs
	}
	for _, sk := range app.Skills.Skills {
		if strings.TrimSpace(sk.Name) == "" || sk.Internal {
			continue
		}
		specs = append(specs, &commandpb.Spec{
			Name:        "/skill:" + strings.TrimPrefix(strings.TrimSpace(sk.Name), "/"),
			Description: sk.Description,
		})
	}
	return specs
}

func agentStatus(option *cfg.Option, app *runner.App) *aop.AgentStatus {
	status := new(aop.AgentStatus)
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
	authority, err := protocols.CanonicalAuthority(option.ServerURL)
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
