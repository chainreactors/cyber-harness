package main

import (
	"fmt"
	"net/url"

	types "github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
	ioaclient "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	ioaserver "github.com/chainreactors/cyber/pkg/exts/ioa/server"
	managementapi "github.com/chainreactors/cyber/pkg/web/api"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/reflect/protoreflect"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
)

// cyber.yaml includes local fields outside the shared settings proto. Preserve
// those fields when projecting settings for the Web editor.

// protoKeyTree holds the keys the shared proto accepts, per message. A nil
// subtree marks an opaque value (list, map or well-known Struct) copied whole.
func protoKeyTree(descriptor protoreflect.MessageDescriptor) map[string]any {
	tree := map[string]any{}
	fields := descriptor.Fields()
	for index := 0; index < fields.Len(); index++ {
		field := fields.Get(index)
		var subtree any
		if field.Kind() == protoreflect.MessageKind && !field.IsMap() && !field.IsList() {
			subtree = protoKeyTree(field.Message())
		}
		tree[string(field.Name())] = subtree
		tree[field.JSONName()] = subtree
	}
	return tree
}

var configurationProtoKeys = protoKeyTree((&types.DistributeConfig{}).ProtoReflect().Descriptor())

// splitDocument returns the proto projection of a document plus the remainder.
func splitDocument(document, schema map[string]any) (map[string]any, map[string]any) {
	canonical, own := map[string]any{}, map[string]any{}
	for key, value := range document {
		subtree, accepted := schema[key]
		fields, isSection := value.(map[string]any)
		nested, wantsSection := subtree.(map[string]any)
		if accepted && isSection && wantsSection {
			// Keep the section even when empty: dropping it would clear the
			// message's presence and make a second save differ from the first.
			projected, rest := splitDocument(fields, nested)
			canonical[key] = projected
			if len(rest) > 0 {
				own[key] = rest
			}
			continue
		}
		if !accepted {
			own[key] = value
			continue
		}
		canonical[key] = value
	}
	return canonical, own
}

// mergeDocument restores local settings onto a projection rewritten from a
// settings-page save.
func mergeDocument(projection, own map[string]any) {
	for key, value := range own {
		fields, isSection := value.(map[string]any)
		target, targetIsSection := projection[key].(map[string]any)
		if isSection && targetIsSection {
			mergeDocument(target, fields)
			continue
		}
		projection[key] = value
	}
}

// ownConfig returns the part of a saved cyber.yaml the shared proto does not
// model. Root keys the extension registry consumes are excluded: those
// round-trip through the proto's extensions map instead.
func ownConfig(data []byte) (map[string]any, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if _, legacy := document["ioa"]; legacy {
		return nil, fmt.Errorf("configuration ioa was removed; use extensions.%s", ioaclient.ConfigKey)
	}
	_, own := splitDocument(document, configurationProtoKeys)
	for _, alias := range defaultSections().Aliases() {
		delete(own, alias)
	}
	return own, nil
}

func validateConfig(config *types.DistributeConfig) error {
	sections := defaultSections()
	for key, fields := range cfg.ValuesFromProto(config.GetExtensions()) {
		if _, err := sections.Decode(key, fields); err != nil {
			return err
		}
	}
	return nil
}
func parseConfig(data []byte) (*types.DistributeConfig, error) {
	// The file's schema is the flags config, not the proto: validate the whole
	// document the way the runtime reads it, then project the settings payload.
	var option cfg.Option
	if err := cfg.LoadConfigBytes(data, &option); err != nil {
		return nil, err
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if _, legacy := document["ioa"]; legacy {
		return nil, fmt.Errorf("configuration ioa was removed; use extensions.%s", ioaclient.ConfigKey)
	}
	projection, _ := splitDocument(document, configurationProtoKeys)
	value, err := cfg.LoadDistributeConfigDocument(projection)
	if err != nil {
		return nil, err
	}
	fields, err := defaultSections().Normalize(document)
	if err != nil {
		return nil, err
	}
	value.Extensions, err = cfg.ValuesToProto(fields)
	if err != nil {
		return nil, err
	}
	if err = validateConfig(value); err != nil {
		return nil, err
	}
	if cfg.HasSingleProviderFields(&option) {
		fileOption, e := (&cfg.Snapshot{Document: document, Sources: map[string]string{}}).FileOptions(defaultSections())
		if e != nil {
			return nil, e
		}
		value.Llm = cfg.LLMFromOption(fileOption)
	}
	cfg.NormalizeLLMConfig(value.Llm)
	return value, nil
}

// projectRuntimeConfig shares the harness projection with other hosts.
func projectRuntimeConfig(option *cfg.Option) (*types.DistributeConfig, error) {
	return cfg.DistributeFromOption(option)
}

// original is the file being replaced; local settings are carried over from it
// because the proto projection cannot express them.
func marshalConfig(config *types.DistributeConfig, original []byte) ([]byte, error) {
	copy := proto.Clone(config).(*types.DistributeConfig)
	data, err := cfg.MarshalDistributeConfigYAML(copy)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	dropNullValues(document)
	own, err := ownConfig(original)
	if err != nil {
		return nil, err
	}
	mergeDocument(document, own)
	return yaml.Marshal(document)
}

// Unset extension values surface as JSON nulls, which would write `token: null`
// into an operator's config where a hand-written file simply omits the key.
// Readers treat null, empty and absent alike, so dropping them changes nothing
// but the file's readability.
func dropNullValues(section map[string]any) {
	for key, value := range section {
		switch typed := value.(type) {
		case nil:
			delete(section, key)
		case map[string]any:
			dropNullValues(typed)
		case []any:
			for _, item := range typed {
				if nested, ok := item.(map[string]any); ok {
					dropNullValues(nested)
				}
			}
		}
	}
}
func configAPI() managementapi.ConfigOptions {
	sections := defaultSections()
	return managementapi.ConfigOptions{Sections: sections, Project: func(config *types.DistributeConfig, view *types.ConfigView) {
		ioaclient.RedactView(view)
		if ext := view.Extensions[ioaserver.ConfigKey]; ext != nil && ext.Values != nil {
			value := ext.Values.Fields["url"].GetStringValue()
			if u, err := url.Parse(value); err == nil {
				u.User = nil
				ext.Values.Fields["url"] = structpb.NewStringValue(u.String())
			}
		}
	}}
}

// Restoring a masked URL keeps saved credentials only for the same endpoint.
func preserveURLCredentials(incoming, current cfg.Values) {
	for _, key := range []string{ioaclient.ConfigKey, ioaserver.ConfigKey} {
		fields := incoming[key]
		if fields == nil {
			continue
		}
		raw, present := fields["url"].(string)
		if !present || raw == "" {
			continue
		}
		previous, _ := current[key]["url"].(string)
		next, err := url.Parse(raw)
		old, oldErr := url.Parse(previous)
		if err == nil && oldErr == nil && next.User == nil && old.User != nil && next.Scheme == old.Scheme && next.Host == old.Host && next.Path == old.Path {
			next.User = old.User
			fields["url"] = next.String()
		}
	}
}
