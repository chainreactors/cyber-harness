package scanner

import (
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/tools/scan"
)

// ScanConfigKey is the scan-pipeline configuration section owned by this
// extension.
const ScanConfigKey = "scan"

// ScanOptions holds scan pipeline settings. Verify stays empty when
// unconfigured so each execution node can resolve model availability.
type ScanOptions struct {
	Verify string `config:"verify" json:"verify"`
}

// ScanSection declares the scan configuration section. It carries no flags.
func ScanSection() cfg.Section {
	return cfg.Section{
		Key: ScanConfigKey, Aliases: []string{ScanConfigKey},
		New:      func() any { return &ScanOptions{} },
		Validate: func(value any) error { return scan.ValidateVerify(value.(*ScanOptions).Verify) },
	}
}

// ReadScan decodes the scan section exactly as configured.
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
