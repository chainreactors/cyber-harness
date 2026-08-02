package runner

import (
	"strings"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
	configpb "github.com/chainreactors/aiscan/pkg/types/config"
)

type RuntimeFeatures struct {
	ProviderEnabled  bool
	ProviderOptional bool
	ToolsEnabled     bool
	AIEnabled        bool
	ScannerAI        bool
	Warning          string
}

// AppConfigFromDistribute builds the runner configuration directly from the
// canonical config proto. Fields that have no proto representation (playwright
// session, uncover credentials, CLI skill paths) stay at their defaults; the
// startup path layers them from cfg.Option via MergeOptionExtras.
func AppConfigFromDistribute(dc *configpb.DistributeConfig, features RuntimeFeatures, logger telemetry.Logger) ApplicationConfig {
	return ApplicationConfig{
		Provider: ApplicationProviderConfig{
			Enabled:   features.ProviderEnabled,
			Config:    ProviderConfigFromProto(dc.GetLlm()),
			Fallbacks: FallbackProviderConfigsFromProto(dc.GetLlm()),
			Optional:  features.ProviderOptional,
		},
		Scanner: ScannerConfig{
			CyberhubURL:  dc.GetCyberhub().GetUrl(),
			CyberhubKey:  dc.GetCyberhub().GetKey(),
			CyberhubMode: dc.GetCyberhub().GetMode(),
			AIEnabled:    features.AIEnabled,
			VerifyMode:   cfg.ResolveString(dc.GetScan().GetVerify(), cfg.DefaultVerify),
			Proxy:        dc.GetCyberhub().GetProxy(),
			FofaEmail:    dc.GetRecon().GetFofaEmail(),
			FofaKey:      dc.GetRecon().GetFofaKey(),
			HunterToken:  dc.GetRecon().GetHunterToken(),
			HunterAPIKey: dc.GetRecon().GetHunterApiKey(),
			ReconProxy:   dc.GetRecon().GetProxy(),
			ReconLimit:   int(dc.GetRecon().GetLimit()),
		},
		Tools: ToolConfig{
			Enabled:       features.ToolsEnabled,
			BashTimeout:   300,
			TavilyKeys:    dc.GetSearch().GetTavilyKeys(),
			OptionalTools: append([]string(nil), dc.GetAgent().GetTools()...),
		},
		Logger: logger,
	}
}

// MergeOptionExtras layers the fields DistributeConfig does not model onto a
// proto-built ApplicationConfig: playwright session, uncover credentials, and
// CLI skill paths.
func MergeOptionExtras(rc ApplicationConfig, option *cfg.Option) ApplicationConfig {
	if option == nil {
		return rc
	}
	rc.Scanner.UncoverCredentials = cloneStringMap(option.UncoverCredentials)
	rc.Tools.PlaywrightSession = option.PlaywrightSession
	rc.CLISkillPaths = skillPathsFromOptions(option)
	return rc
}

func AppConfig(option *cfg.Option, features RuntimeFeatures, logger telemetry.Logger) ApplicationConfig {
	return ApplicationConfig{
		Provider: ApplicationProviderConfig{
			Enabled:   features.ProviderEnabled,
			Config:    ProviderConfig(option),
			Fallbacks: FallbackProviderConfigs(option),
			Optional:  features.ProviderOptional,
		},
		Scanner: ScannerConfig{
			CyberhubURL:        option.CyberhubURL,
			CyberhubKey:        option.CyberhubKey,
			CyberhubMode:       option.CyberhubMode,
			AIEnabled:          features.AIEnabled,
			VerifyMode:         cfg.ResolveString(option.ScanConfig.Verify, cfg.DefaultVerify),
			Proxy:              option.Proxy,
			FofaEmail:          option.FofaEmail,
			FofaKey:            option.FofaKey,
			HunterToken:        option.HunterToken,
			HunterAPIKey:       option.HunterAPIKey,
			ReconProxy:         option.ReconProxy,
			ReconLimit:         intOptionValue(option.ReconLimit),
			UncoverCredentials: cloneStringMap(option.UncoverCredentials),
		},
		Tools: ToolConfig{
			Enabled:           features.ToolsEnabled,
			BashTimeout:       300,
			TavilyKeys:        resolveTavilyKeys(option.TavilyKey, option.SearchConfig.TavilyKeys, cfg.DefaultTavilyKeys),
			PlaywrightSession: option.PlaywrightSession,
			OptionalTools:     option.Tools,
		},
		Logger:        logger,
		CLISkillPaths: skillPathsFromOptions(option),
	}
}

func skillPathsFromOptions(option *cfg.Option) []string {
	var paths []string
	for _, s := range option.Skills {
		if looksLikePath(s) {
			paths = append(paths, s)
		}
	}
	return paths
}

func looksLikePath(s string) bool {
	return strings.ContainsAny(s, `/\`) || strings.HasPrefix(s, ".")
}

func intOptionValue(p *int) int {
	if p != nil {
		return *p
	}
	return 0
}

func resolveTavilyKeys(primary string, fallbacks ...string) string {
	keys := make([]string, 0, len(fallbacks)+1)
	for _, raw := range append([]string{primary}, fallbacks...) {
		raw = strings.TrimSpace(raw)
		if raw != "" {
			keys = append(keys, raw)
		}
	}
	return strings.Join(keys, ",")
}

func cloneStringMap(src map[string]string) map[string]string {
	if len(src) == 0 {
		return nil
	}
	dst := make(map[string]string, len(src))
	for key, value := range src {
		dst[key] = value
	}
	return dst
}
