package node

import (
	"fmt"
	"os"
	"os/user"
	"runtime"
	"strings"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	types "github.com/chainreactors/cyber/core/types"
	apppkg "github.com/chainreactors/cyber/pkg/app"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"google.golang.org/protobuf/types/known/structpb"
)

// DefaultRuntimeInfo returns OS process metadata for AOP node registration.
func DefaultRuntimeInfo() *aop.AgentRuntimeInfo {
	metadata, _ := structpb.NewStruct(map[string]any{"client": "cyber"})
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

// CommandSpecs returns a node's user-facing commands: "/verb" runtime and
// skill commands plus every "!verb" registered in the node's command registry.
func CommandSpecs(runtime *session.Runtime) []*types.CommandSpec {
	if runtime == nil {
		return nil
	}
	return commandSpecs(runtime, runtime.CommandSpecs(true))
}

func commandSpecs(runtime *session.Runtime, specs []*types.CommandSpec) []*types.CommandSpec {
	store := runtime.Skills()
	specs = append(specs, RegistryCommandSpecs(runtime.CommandRegistry(), store)...)
	if store == nil {
		return specs
	}
	for _, skill := range store.All() {
		if strings.TrimSpace(skill.Name) == "" || skill.Internal {
			continue
		}
		specs = append(specs, &types.CommandSpec{
			Name:        "/skill:" + strings.TrimPrefix(strings.TrimSpace(skill.Name), "/"),
			Description: skill.Description,
		})
	}
	return specs
}

// RegistryCommandSpecs projects the Bash-internal command registry without
// adding chat runtime or skill commands. Tool-only nodes use these specs too.
func RegistryCommandSpecs(registry coretool.CommandExecutor, store *skills.Store) []*types.CommandSpec {
	if registry == nil {
		return nil
	}
	all := registry.All()
	specs := make([]*types.CommandSpec, 0, len(all))
	for _, command := range all {
		if spec := registryCommandSpec(store, command, registry.DescriptionPath(command.Name)); spec != nil {
			specs = append(specs, spec)
		}
	}
	return specs
}

func registryCommandSpec(store *skills.Store, command *types.CommandSpec, descriptionPath string) *types.CommandSpec {
	name := strings.TrimSpace(command.Name)
	if name == "" {
		return nil
	}
	return &types.CommandSpec{
		Name:        "!" + name,
		Usage:       commandUsage(command.Usage, name),
		Description: commandDescription(store, descriptionPath),
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

// AgentStatus reports Agent provider/model health; the Profile adds its status.
func AgentStatus(app *apppkg.State) *aop.AgentStatus {
	status := new(aop.AgentStatus)
	if app != nil {
		_, providerConfig := app.ProviderState()
		status.Provider = providerConfig.Provider
		status.Model = providerConfig.Model
		health := app.ProviderHealth()
		if health.State == provider.HealthFailed || (health.State == provider.HealthNotConfigured && health.Error != "") {
			status.ConfigError = statusOneLine(health.Error, 240)
		}
	}
	return status
}

// ReloadConfig hot-swaps the LLM provider from a pushed protobuf config.
func ReloadConfig(distribute *types.DistributeConfig, runtime *session.Runtime, option *cfg.Option, logger telemetry.Logger) (agent.Provider, string, error) {
	if runtime == nil {
		return nil, "", fmt.Errorf("agent runtime is not configured")
	}
	if logger == nil {
		logger = telemetry.NopLogger()
	}
	if distribute == nil {
		return nil, "", fmt.Errorf("remote config is required")
	}
	providerConfig := apppkg.ProviderConfigFromProto(distribute.GetLlm())
	provider, resolved, err := runtime.ReloadResolvedProvider(providerConfig)
	if err != nil {
		return nil, "", err
	}
	model := resolved.Model
	if option != nil {
		apppkg.ApplyResolvedProviderOptions(option, resolved)
	}
	logger.Importantf("config reloaded: provider=%s model=%s", provider.Name(), model)
	return provider, model, nil
}

func statusOneLine(value string, limit int) string {
	value = strings.Join(strings.Fields(value), " ")
	runes := []rune(value)
	if len(runes) > limit && limit > 3 {
		return string(runes[:limit-3]) + "..."
	}
	return value
}
