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
	"github.com/chainreactors/aiscan/pkg/commands"
	types "github.com/chainreactors/aiscan/pkg/types"
	"github.com/chainreactors/aiscan/skills"
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

// CommandCatalog is a node's user-facing composer catalog: "/verb" runtime and
// skill commands plus every "!verb" registered in the node's command registry.
// The web splits the two prefixes into their respective popups, while both stay
// sourced from the same node-level catalog used by the TUI.
func CommandCatalog(app *App) []*types.CommandSpec {
	specs := RuntimeCommandSpecs()
	if app != nil {
		specs = append(specs, RegistryCommandCatalog(app.Commands, app.Skills)...)
	}
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

// RegistryCommandCatalog projects the Bash-internal command registry without
// adding chat runtime or skill commands. Tool-only nodes use this catalog too.
func RegistryCommandCatalog(registry *commands.CommandRegistry, store *skills.Store) []*types.CommandSpec {
	if registry == nil {
		return nil
	}
	all := registry.All()
	specs := make([]*types.CommandSpec, 0, len(all))
	for _, command := range all {
		if spec := registryCommandSpec(store, command); spec != nil {
			specs = append(specs, spec)
		}
	}
	return specs
}

func registryCommandSpec(store *skills.Store, command commands.Command) *types.CommandSpec {
	name := strings.TrimSpace(command.Name)
	if name == "" {
		return nil
	}
	return &types.CommandSpec{
		Name:        "!" + name,
		Usage:       commandUsage(command.Usage, name),
		Description: commandDescription(store, command.DescriptionPath),
	}
}

func commandUsage(raw, name string) string {
	lines := strings.Split(strings.ReplaceAll(raw, "\r\n", "\n"), "\n")
	for index, line := range lines {
		line = strings.TrimSpace(line)
		if !strings.HasPrefix(strings.ToLower(line), "usage:") {
			continue
		}
		if value := strings.TrimSpace(line[len("usage:"):]); value != "" {
			return commandUsageLine(value, name)
		}
		for _, next := range lines[index+1:] {
			if next = strings.TrimSpace(next); next != "" {
				return commandUsageLine(next, name)
			}
		}
	}
	for _, line := range lines {
		line = strings.TrimSpace(line)
		if line == name || strings.HasPrefix(line, name+" ") || strings.HasPrefix(line, name+" -") || strings.HasPrefix(line, name+" —") {
			return commandUsageLine(line, name)
		}
	}
	return "!" + name
}

func commandUsageLine(line, name string) string {
	if strings.HasPrefix(line, name) {
		return "!" + line
	}
	return "!" + name
}

func commandDescription(store *skills.Store, location string) string {
	location = strings.TrimSpace(location)
	if store == nil || location == "" {
		return ""
	}
	raw, handled, err := store.ReadVirtual(location)
	if err != nil || !handled {
		return ""
	}
	frontmatter, _ := skills.ParseFrontmatter(raw)
	return strings.TrimSpace(frontmatter.Description)
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
