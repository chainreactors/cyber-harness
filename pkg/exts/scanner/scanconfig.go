package scanner

import (
	cfg "github.com/chainreactors/cyber/pkg/config"
)

// ScanConfigKey is the scan-pipeline configuration section owned by this
// extension.
const ScanConfigKey = "scan"

// ScanOptions holds scan pipeline settings. Verify stays empty when
// unconfigured; the consumption site resolves DefaultVerify, so the
// distributed configuration never invents a value.
type ScanOptions struct {
	Verify string `config:"verify" json:"verify"`
}

// ScanSection declares the scan configuration section. It carries no flags.
func ScanSection() cfg.Section {
	return cfg.Section{
		Key: ScanConfigKey, Aliases: []string{ScanConfigKey},
		New: func() any { return &ScanOptions{} },
	}
}

// ReadScan decodes the scan section exactly as configured: Verify stays empty
// when unconfigured so distributed configuration never invents a value.
func ReadScan(option *cfg.Option) (ScanOptions, error) {
	if option == nil {
		return ScanOptions{}, nil
	}
	if option.Resolved != nil {
		decoded, err := cfg.Get[*ScanOptions](option.Resolved, ScanConfigKey)
		if err != nil {
			return ScanOptions{}, err
		}
		return *decoded, nil
	}
	registry := cfg.NewSections()
	if _, err := registry.Add(ScanSection()); err != nil {
		return ScanOptions{}, err
	}
	raw, err := registry.Decode(ScanConfigKey, option.Extensions[ScanConfigKey])
	if err != nil {
		return ScanOptions{}, err
	}
	return *raw.(*ScanOptions), nil
}

// ReadVerify returns the configured verification level or DefaultVerify.
func ReadVerify(option *cfg.Option) string {
	if option == nil {
		return DefaultVerify
	}
	if option.Resolved != nil {
		if decoded, err := cfg.Get[*ScanOptions](option.Resolved, ScanConfigKey); err == nil {
			return cfg.ResolveString(decoded.Verify, DefaultVerify)
		}
	}
	if fields := option.Extensions[ScanConfigKey]; fields != nil {
		if verify, ok := fields["verify"].(string); ok {
			return cfg.ResolveString(verify, DefaultVerify)
		}
	}
	return DefaultVerify
}
