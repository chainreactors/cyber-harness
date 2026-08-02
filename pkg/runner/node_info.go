package runner

import (
	"fmt"
	"os"
	"os/user"
	"runtime"
	"strings"

	"github.com/chainreactors/aiscan/agent"
	aop "github.com/chainreactors/aiscan/aop"
	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	types "github.com/chainreactors/aiscan/pkg/types"
	"google.golang.org/protobuf/types/known/structpb"
)

// DefaultRuntimeInfo returns OS process metadata for AOP node registration.
func DefaultRuntimeInfo() *aop.AgentRuntimeInfo {
	metadata, _ := structpb.NewStruct(map[string]any{"client": "aiscan"})
	runtimeInfo := &aop.AgentRuntimeInfo{
		Os:       runtime.GOOS,
		Arch:     runtime.GOARCH,
		Pid:      int32(os.Getpid()),
		Metadata: metadata,
	}
	if host, err := os.Hostname(); err == nil {
		runtimeInfo.Hostname = host
	}
	if wd, err := os.Getwd(); err == nil {
		runtimeInfo.WorkingDir = wd
	}
	if current, err := user.Current(); err == nil && current != nil {
		runtimeInfo.Username = current.Username
	}
	return runtimeInfo
}

// CommandCatalog is a node's user-facing "/verb" catalog: the static
// agent-scope menu commands plus one per loaded (and non-internal) skill. The
// hub merges it with its hub-scope commands to build the web "/" menu and
// /help, so the menu reflects what this node can run.
func CommandCatalog(app *App) []*types.CommandSpec {
	specs := RuntimeCommandSpecs()
	if app == nil || app.Skills == nil {
		return specs
	}
	for _, sk := range app.Skills.Skills {
		if strings.TrimSpace(sk.Name) == "" || sk.Internal {
			continue
		}
		specs = append(specs, &types.CommandSpec{
			Name:        "/skill:" + strings.TrimPrefix(strings.TrimSpace(sk.Name), "/"),
			Description: sk.Description,
		})
	}
	return specs
}

// AgentStatus reports the node's provider/model/IOA binding for pool views.
func AgentStatus(option *cfg.Option, app *App) *aop.AgentStatus {
	status := new(aop.AgentStatus)
	if option != nil {
		status.Space = option.Space
	}
	if app != nil {
		status.Provider = app.ProviderConfig.Provider
		status.Model = app.ProviderConfig.Model
		status.Bound = app.IOAClient != nil && app.IOAClient.Bound()
	}
	return status
}

// ReloadRuntimeConfig hot-swaps the LLM provider from a pushed protobuf
// config. A build failure leaves the current provider in place and is
// reported through the returned error.
func ReloadRuntimeConfig(distribute *types.DistributeConfig, rt *AgentRuntime, app *App, option *cfg.Option, logger telemetry.Logger) (agent.Provider, string, error) {
	if rt == nil {
		return nil, "", fmt.Errorf("agent runtime is not configured")
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	if distribute == nil {
		return nil, "", fmt.Errorf("remote config is required")
	}
	providerConfig := ProviderConfigFromProto(distribute.GetLlm())
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
		ApplyResolvedProviderOptions(option, *resolved)
	}
	logger.Importantf("config reloaded: provider=%s model=%s", provider.Name(), model)
	return provider, model, nil
}
