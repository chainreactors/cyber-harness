package scanner

import (
	cfg "github.com/chainreactors/cyber/pkg/config"
)

// CyberhubConfigKey is the configuration section owned by this extension.
// Hosts without the scanner extension reject the cyberhub YAML key.
const CyberhubConfigKey = "cyberhub"

// CyberhubOptions configures the cyberhub resource service and the scanner
// egress defaults.
type CyberhubOptions struct {
	URL   string `long:"cyberhub-url" config:"url" json:"url" description:"Cyberhub server URL for loading fingers/templates"`
	Key   string `long:"cyberhub-key" config:"key" json:"key" description:"Cyberhub API key"`
	Mode  string `long:"cyberhub-mode" config:"mode" json:"mode" description:"Cyberhub resource mode: merge or override"`
	Proxy string `long:"proxy" config:"proxy" json:"proxy" description:"Proxy for scanner tools. Supports socks5://, trojan://, vless://, clash:// (subscription with load balancing)"`
	Mitm  *bool  `long:"mitm" config:"mitm" json:"mitm" init_default:"true" config_optional:"true" description:"Record tool traffic through the MITM hub (default: enabled). Disable for pure proxy routing without interception/capture"`
}

// CyberhubSection declares the cyberhub configuration section, including the
// CYBER_CYBERHUB_* / CYBER_PROXY environment mapping.
func CyberhubSection() cfg.Section {
	return cfg.Section{
		Key: CyberhubConfigKey, Aliases: []string{CyberhubConfigKey},
		New: func() any {
			return &CyberhubOptions{
				URL: DefaultCyberhubURL, Key: DefaultCyberhubKey,
				Mode: cfg.ResolveString(DefaultCyberhubMode, "merge"), Proxy: DefaultScannerProxy,
			}
		},
		Secrets:     []string{"key"},
		Environment: cyberhubEnvironment,
	}
}

// CyberhubFlagGroup contributes the scanner flags to a command.
func CyberhubFlagGroup() cfg.FlagGroup {
	return cfg.FlagGroup{Name: "Scanner Options", Options: &CyberhubOptions{}}
}

func cyberhubEnvironment(sources cfg.Sources) (map[string]any, map[string]any, error) {
	overrides := map[string]any{}
	set := func(field string, name string) {
		if _, explicit := sources.CLI[field]; explicit {
			return
		}
		if value, ok := sources.LookupEnv(name); ok && value != "" {
			overrides[field] = value
		}
	}
	set("url", "CYBER_CYBERHUB_URL")
	set("key", "CYBER_CYBERHUB_KEY")
	set("mode", "CYBER_CYBERHUB_MODE")
	set("proxy", "CYBER_PROXY")
	if len(overrides) == 0 {
		return nil, nil, nil
	}
	return overrides, nil, nil
}

// ReadCyberhub decodes the cyberhub section from a parsed option set.
func ReadCyberhub(option *cfg.Option) (CyberhubOptions, error) {
	if option == nil {
		return *newCyberhubDefaults(), nil
	}
	if option.Resolved != nil {
		decoded, err := cfg.Get[*CyberhubOptions](option.Resolved, CyberhubConfigKey)
		if err != nil {
			return CyberhubOptions{}, err
		}
		return *decoded, nil
	}
	registry := cfg.NewSections()
	if _, err := registry.Add(CyberhubSection()); err != nil {
		return CyberhubOptions{}, err
	}
	raw, err := registry.Decode(CyberhubConfigKey, option.Extensions[CyberhubConfigKey])
	if err != nil {
		return CyberhubOptions{}, err
	}
	return *raw.(*CyberhubOptions), nil
}

func newCyberhubDefaults() *CyberhubOptions {
	return CyberhubSection().New().(*CyberhubOptions)
}
