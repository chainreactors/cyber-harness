package config

import (
	"fmt"
	"maps"
	"reflect"

	types "github.com/chainreactors/cyber/core/types"
)

// ResolveDistributedRuntime builds a fresh snapshot through the same schema,
// precedence and validation used at startup. Host identity and transport stay
// local; all distributed sections are replaced, including removed extensions.
// The server's LLM settings are authoritative over node flags and environment.
func ResolveDistributedRuntime(distributed *types.DistributeConfig, host *Option) (*Option, error) {
	if distributed == nil || host == nil {
		return nil, fmt.Errorf("distributed configuration and host options are required")
	}
	data, err := MarshalDistributeConfigYAML(distributed)
	if err != nil {
		return nil, err
	}
	loaded := Option{Sections: host.Sections}
	if err := LoadConfigBytes(data, &loaded); err != nil {
		return nil, err
	}
	option := *host
	// A distributed replacement has no local file layers. Do not share the host's
	// mutable inspection state or use its file precedence to resolve remote values.
	option.Snapshot = nil
	useRemoteLLM(&option)
	explicit := explicitOptions(&option)
	mergeOptions(&option, &loaded, true)
	if err := finishRuntimeConfig(&option, &explicit); err != nil {
		return nil, err
	}
	return &option, nil
}

// useRemoteLLM drops only local model values and their CLI precedence markers.
// Clone the map because distributed resolution must not change the host options.
func useRemoteLLM(option *Option) {
	option.remoteLLM = true
	option.LLMOptions = LLMOptions{AI: option.AI}
	option.Explicit = maps.Clone(option.Explicit)
	fields := reflect.TypeFor[LLMOptions]()
	for i := 0; i < fields.NumField(); i++ {
		field := fields.Field(i)
		if field.Tag.Get("config") == "" {
			continue
		}
		delete(option.Explicit, field.Name)
		delete(option.Explicit, field.Tag.Get("long"))
		delete(option.Explicit, field.Tag.Get("short"))
	}
}
