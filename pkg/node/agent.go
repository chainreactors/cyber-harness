package node

import (
	"context"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/chainreactors/cyber/agent"
	agentsession "github.com/chainreactors/cyber/agent/session"
	aop "github.com/chainreactors/cyber/aop"
	filepb "github.com/chainreactors/cyber/aop/file"
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/telemetry"
	types "github.com/chainreactors/cyber/core/types"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	"github.com/chainreactors/cyber/pkg/console"
	profile "github.com/chainreactors/cyber/pkg/profile"
)

func RunWebSocket(ctx context.Context, factory profile.Factory, option *cfg.Option, logger telemetry.Logger) error {
	return runRemoteAgent(ctx, factory, option, logger)
}

func runRemoteAgent(ctx context.Context, factory profile.Factory, option *cfg.Option, logger telemetry.Logger) error {
	if err := resolveRemoteAgentURLs(option); err != nil {
		return err
	}
	nodeID, err := webNodeID(option)
	if err != nil {
		return err
	}

	p, err := factory.Build(profile.Request{
		Option: option, ProviderMode: profile.ProviderOptional, Logger: logger,
		Session: &agentsession.Config{PrimarySessionID: console.MainREPLName, Loop: agent.StandardLoop{}},
	})
	if err != nil {
		return err
	}
	if err := p.Load(ctx); err != nil {
		_ = p.Close(context.Background())
		return err
	}
	defer p.Close(context.Background())
	application, err := p.App()
	if err != nil {
		return err
	}
	_, providerConfig := application.ProviderState()
	apppkg.ApplyResolvedProviderOptions(option, providerConfig)
	rt, err := p.Runtime()
	if err != nil {
		return err
	}
	repl, err := console.StartPersistent(rt, option, p.ConsoleBindings())
	if err != nil {
		return err
	}
	defer repl.Close()

	chatHandler := &chatAgentHandler{
		rt:     rt,
		app:    application,
		option: option,
		logger: logger,
		ready:  make(chan struct{}),
		status: p.AgentStatus,
	}

	connectionDone := make(chan struct{})
	go func() {
		defer close(connectionDone)
		dialURL, _ := SplitAccessKey(option.ServerURL)
		logger.Debugf("websocket transport connection to %s", dialURL)

		connection := connectionConfig{
			ServerURL:          option.ServerURL,
			Name:               rt.NodeName(),
			Registry:           rt.CommandRegistry(),
			Executor:           rt.Tools(),
			Agent:              rt,
			Progress:           application.Progress,
			Hooks:              rt.Hooks(),
			Logger:             logger,
			Chat:               chatHandler,
			NodeID:             nodeID,
			Runtime:            DefaultRuntimeInfo(),
			Status:             p.AgentStatus,
			Menu:               func() []*types.CommandSpec { return CommandSpecs(rt) },
			RegisterNamespaces: p.RegisterNamespaces,
		}
		_ = connect(ctx, connection)
	}()

	if provider, _ := application.ProviderState(); provider == nil {
		select {
		case <-chatHandler.ready:
		case <-ctx.Done():
			<-connectionDone
			return nil
		}
	}
	if provider, _ := application.ProviderState(); provider == nil {
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

	_, err = rt.EnsureSession(agentsession.SessionOptions{ID: "startup"})
	if err != nil {
		return err
	}
	run, err := rt.RunSession(ctx, "startup", agentsession.RunInput{TurnID: "startup", Content: []*aop.Content{aop.Text(task)}})
	if err == nil {
		_, err = run.Wait()
	}
	_ = rt.CloseSession(context.Background(), "startup", agentsession.SessionCloseCompleted)

	<-connectionDone
	return err
}

func resolveRemoteAgentURLs(option *cfg.Option) error {
	if option == nil {
		return fmt.Errorf("web node configuration is required")
	}
	if err := cfg.ResolveAgentServerURLs(option); err != nil {
		return fmt.Errorf("resolve remote agent URLs: %w", err)
	}
	return nil
}

// ---------------------------------------------------------------------------
// chatAgentHandler implements the connection's upload and config-reload hooks.
// AOP core/command handlers are registered on the existing connection mux.
// ---------------------------------------------------------------------------

type chatAgentHandler struct {
	rt        *agentsession.Runtime
	app       *apppkg.App
	option    *cfg.Option
	logger    telemetry.Logger
	ready     chan struct{}
	readyOnce sync.Once
	status    func() *aop.AgentStatus
}

func (h *chatAgentHandler) Upload(req *filepb.UploadRequest) (*filepb.Result, error) {
	if req == nil {
		return nil, fmt.Errorf("upload request is required")
	}
	filename := filepath.Base(strings.TrimSpace(req.Filename))
	if filename == "." || filename == "" {
		filename = "upload"
	}
	dir := filepath.Join(os.TempDir(), "cyber-uploads")
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
	provider, model, err := ReloadConfig(config, h.rt, h.option, h.logger)
	result := &types.ReloadResult{Ok: err == nil, Model: model}
	if err != nil {
		result.Error = err.Error()
		return result, nil
	}
	result.Provider = provider.Name()
	if h.status != nil {
		return result, h.status()
	}
	return result, AgentStatus(h.app)
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
	if nodeID := strings.TrimSpace(option.NodeID); nodeID != "" {
		return nodeID, nil
	}
	if nodeID := strings.TrimSpace(option.NodeName); nodeID != "" {
		return nodeID, nil
	}
	return "", fmt.Errorf("node_id is required; set --node-id or --node-name")
}
