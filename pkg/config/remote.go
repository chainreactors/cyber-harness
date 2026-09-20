package config

import (
	"fmt"

	types "github.com/chainreactors/cyber/core/types"
)

// ResolveDistributedRuntime builds a fresh snapshot through the same schema,
// precedence and validation used at startup. Host identity and transport stay
// local; all distributed sections are replaced, including removed extensions.
func ResolveDistributedRuntime(distributed *types.DistributeConfig, host *Option) (*Option, error) {
	if distributed == nil || host == nil {
		return nil, fmt.Errorf("distributed configuration and host options are required")
	}
	data, err := MarshalDistributeConfigYAML(distributed)
	if err != nil {
		return nil, err
	}
	explicit := explicitOptions(host)
	loaded := Option{Sections: host.Sections}
	if err := LoadConfigBytes(data, &loaded); err != nil {
		return nil, err
	}
	option := explicit
	option.MiscOptions, option.OutputOptions = host.MiscOptions, host.OutputOptions
	mergeOption(&option, &loaded)
	if err := finishRuntimeConfig(&option, &explicit); err != nil {
		return nil, err
	}
	option.NodeOptions = host.NodeOptions
	option.ServerURL, option.Transport = host.ServerURL, host.Transport
	return &option, nil
}
