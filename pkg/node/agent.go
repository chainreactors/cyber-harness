package node

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	aop "github.com/chainreactors/aiscan/aop"
	filepb "github.com/chainreactors/aiscan/aop/file"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	coreterminal "github.com/chainreactors/aiscan/core/terminal"
	"github.com/chainreactors/aiscan/pkg/runner"
	types "github.com/chainreactors/aiscan/pkg/types"
)

func RunWebSocket(ctx context.Context, option *cfg.Option, logger telemetry.Logger) error {
	return runRemoteAgent(ctx, option, logger)
}

func runRemoteAgent(ctx context.Context, option *cfg.Option, logger telemetry.Logger) error {
	nodeID, err := webNodeID(option)
	if err != nil {
		return err
	}

	appConfig := runner.AppConfig(option, runner.RuntimeFeatures{
		ProviderEnabled: true, ProviderOptional: true, ToolsEnabled: true, AIEnabled: true,
	}, logger)
	appConfig.IOA = remoteIOAConfig(option)
	application, err := runner.NewApp(ctx, appConfig)
	if err != nil {
		return err
	}
	defer application.Close()
	runner.ApplyResolvedProviderOptions(option, application.ProviderConfig)
	rt, err := runner.NewAgentRuntime(ctx, option, logger, &runner.RuntimeConfig{
		ExistingApp: application, REPLMode: runner.REPLPersistent,
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
			AgentRuntime:   rt,
			NodeID:         nodeID,
			Runtime:        runner.DefaultRuntimeInfo(),
			Status:         func() *aop.AgentStatus { return runner.AgentStatus(option, application) },
			Menu:           func() []*types.CommandSpec { return runner.CommandCatalog(application) },
			PTYRouter:      func() (*coreterminal.Router, error) { return NewPTYRouter(application.Commands), nil },
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
// chatAgentHandler implements the connection's upload and config-reload hooks.
// AOP core/command messages are dispatched by rt.HandleEnvelope directly.
// ---------------------------------------------------------------------------

type chatAgentHandler struct {
	rt        *runner.AgentRuntime
	app       *runner.App
	option    *cfg.Option
	logger    telemetry.Logger
	ready     chan struct{}
	readyOnce sync.Once
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

func (h *chatAgentHandler) ReloadConfig(config *types.DistributeConfig) (*types.ReloadResult, *aop.AgentStatus) {
	defer h.readyOnce.Do(func() {
		if h.ready != nil {
			close(h.ready)
		}
	})
	provider, model, err := runner.ReloadRuntimeConfig(config, h.rt, h.app, h.option, h.logger)
	result := &types.ReloadResult{Ok: err == nil, Model: model}
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	result.Provider = provider.Name()
	return result, runner.AgentStatus(h.option, h.app)
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

func webNodeID(option *cfg.Option) (string, error) {
	if option == nil {
		return "", fmt.Errorf("web node configuration is required")
	}
	if nodeID := strings.TrimSpace(option.IOANodeID); nodeID != "" {
		return nodeID, nil
	}
	if nodeID := strings.TrimSpace(option.IOANodeName); nodeID != "" {
		return nodeID, nil
	}
	return "", fmt.Errorf("node_id is required; set --node-id or --node-name")
}

func remoteIOAConfig(option *cfg.Option) *runner.IOAConfig {
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
	}
}
