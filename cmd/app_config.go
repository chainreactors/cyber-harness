package cmd

import (
	"crypto/rand"
	"encoding/hex"
	"strconv"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/app"
	"github.com/chainreactors/aiscan/pkg/provider"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

type runtimeFeatures struct {
	ProviderEnabled  bool
	ProviderOptional bool
	ToolsEnabled     bool
	AIEnabled        bool
	ScannerAI        bool
	Warning          string
}

func appConfig(option *Option, features runtimeFeatures, logger telemetry.Logger) app.Config {
	return app.Config{
		Provider: app.ProviderConfig{
			Enabled:  features.ProviderEnabled,
			Config:   providerConfig(option),
			Optional: features.ProviderOptional,
		},
		Vision: app.ProviderConfig{
			Enabled: visionHasProviderConfig(option),
			Config:  visionProviderConfig(option),
		},
		Scanner: app.ScannerConfig{
			CyberhubURL:  option.CyberhubURL,
			CyberhubKey:  option.CyberhubKey,
			CyberhubMode: option.CyberhubMode,
			AIEnabled:    features.AIEnabled,
			AITimeout:    defaultInt(DefaultVerifyTimeout, 120),
		},
		Tools: app.ToolConfig{
			Enabled:       features.ToolsEnabled,
			BashTimeout:   300,
			VisionEnabled: visionEnabled(option),
		},
		Logger: logger,
	}
}

func providerConfig(option *Option) provider.ProviderConfig {
	cfg := defaultProviderConfig()
	if option.Provider != "" {
		cfg.Provider = option.Provider
	} else if inferred := inferProviderFromBaseURL(option.BaseURL); inferred != "" {
		cfg.Provider = inferred
	}
	if option.BaseURL != "" {
		cfg.BaseURL = option.BaseURL
	}
	if option.APIKey != "" {
		cfg.APIKey = option.APIKey
	}
	if option.Model != "" {
		cfg.Model = option.Model
	}
	if option.Proxy != "" {
		cfg.Proxy = option.Proxy
	}
	cfg.Timeout = 120
	return cfg
}

func visionEnabled(option *Option) bool {
	return option.Vision || visionHasProviderConfig(option)
}

func visionHasProviderConfig(option *Option) bool {
	return option.VisionProvider != "" ||
		option.VisionBaseURL != "" ||
		option.VisionAPIKey != "" ||
		option.VisionModel != "" ||
		option.VisionProxy != ""
}

func visionProviderConfig(option *Option) provider.ProviderConfig {
	cfg := provider.ProviderConfig{}
	if option.VisionProvider != "" {
		cfg.Provider = option.VisionProvider
	} else if inferred := inferProviderFromBaseURL(option.VisionBaseURL); inferred != "" {
		cfg.Provider = inferred
	}
	if option.VisionBaseURL != "" {
		cfg.BaseURL = option.VisionBaseURL
	}
	if option.VisionAPIKey != "" {
		cfg.APIKey = option.VisionAPIKey
	}
	if option.VisionModel != "" {
		cfg.Model = option.VisionModel
	}
	if option.VisionProxy != "" {
		cfg.Proxy = option.VisionProxy
	}
	cfg.Timeout = 120
	return cfg
}

func applyResolvedProviderOptions(option *Option, cfg provider.ProviderConfig) {
	option.Provider = cfg.Provider
	option.BaseURL = cfg.BaseURL
	option.APIKey = cfg.APIKey
	option.Model = cfg.Model
	option.Proxy = cfg.Proxy
}

func applyDefaults(option *Option) {
	option.CyberhubURL = resolveString(option.CyberhubURL, DefaultCyberhubURL)
	option.CyberhubKey = resolveString(option.CyberhubKey, DefaultCyberhubKey)
	option.CyberhubMode = resolveString(resolveString(option.CyberhubMode, DefaultCyberhubMode), "merge")
	option.IOAURL = resolveString(option.IOAURL, DefaultIOAURL)
	option.IOANodeID = resolveString(option.IOANodeID, DefaultIOANodeID)
	option.IOANodeName = resolveString(option.IOANodeName, DefaultIOANodeName)
	option.Space = resolveSpace(option.Space)
	if option.Model == "" {
		option.Model = resolveString(DefaultModel, "deepseek-v4-pro")
	}
}

func resolveString(value, fallback string) string {
	if value != "" {
		return value
	}
	return fallback
}

func inferProviderFromBaseURL(baseURL string) string {
	baseURL = strings.ToLower(strings.TrimSpace(baseURL))
	switch {
	case strings.Contains(baseURL, "deepseek.com"):
		return "deepseek"
	case strings.Contains(baseURL, "openrouter.ai"):
		return "openrouter"
	case strings.Contains(baseURL, "groq.com"):
		return "groq"
	case strings.Contains(baseURL, "moonshot.cn"):
		return "moonshot"
	case strings.Contains(baseURL, "localhost:11434"), strings.Contains(baseURL, "127.0.0.1:11434"):
		return "ollama"
	default:
		return ""
	}
}

func defaultVerifyMode() string {
	value := strings.ToLower(strings.TrimSpace(DefaultVerify))
	if value == "" {
		return "off"
	}
	return value
}

func defaultInt(value string, fallback int) int {
	value = strings.TrimSpace(value)
	if value == "" {
		return fallback
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return fallback
	}
	return parsed
}

func resolveSpace(space string) string {
	if space != "" && space != "default" {
		return space
	}
	if DefaultSpace != "" {
		return DefaultSpace
	}
	if space != "" {
		return space
	}
	return "default"
}

func defaultIOANodeName(option *Option) string {
	if option.IOANodeName != "" {
		return option.IOANodeName
	}
	var b [4]byte
	if _, err := rand.Read(b[:]); err == nil {
		return "aiscan-" + hex.EncodeToString(b[:])
	}
	return "aiscan-" + strconv.FormatInt(time.Now().UnixNano(), 36)
}
