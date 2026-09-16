package main

import (
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/resource"
	client "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	ioaprobe "github.com/chainreactors/cyber/pkg/exts/ioa/client/probe"
	server "github.com/chainreactors/cyber/pkg/exts/ioa/server"
	scannerprobe "github.com/chainreactors/cyber/pkg/exts/scanner/probe"
	searchprobe "github.com/chainreactors/cyber/pkg/exts/search/probe"
	"github.com/chainreactors/cyber/pkg/probe"
	types "github.com/chainreactors/cyber/pkg/types"
	managementapi "github.com/chainreactors/cyber/pkg/web/api"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
	"net/url"
)

func normalizeProductConfig(config *types.DistributeConfig) error { //nolint:unused // used by the full-tag web build
	return client.NormalizeConfig(config)
}
func validateProductConfig(config *types.DistributeConfig) error {
	return client.ValidateWire(config, productSections(false))
}
func parseProductConfig(data []byte) (*types.DistributeConfig, error) {
	value, err := cfg.LoadDistributeConfigYAML(data)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err = yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	fields, err := productSections(false).Normalize(document)
	if err != nil {
		return nil, err
	}
	value.Extensions, err = cfg.ValuesToProto(fields)
	if err != nil {
		return nil, err
	}
	value.Ioa = nil
	if err = validateProductConfig(value); err != nil {
		return nil, err
	}
	cfg.NormalizeLLMConfig(value.Llm)
	return value, nil
}
func marshalProductConfig(config *types.DistributeConfig) ([]byte, error) {
	copy := proto.Clone(config).(*types.DistributeConfig)
	fields := cfg.ValuesFromProto(copy.Extensions)[client.ConfigKey]
	copy.Ioa = nil
	delete(copy.Extensions, client.ConfigKey)
	data, err := cfg.MarshalDistributeConfigYAML(copy)
	if err != nil {
		return nil, err
	}
	var document map[string]any
	if err := yaml.Unmarshal(data, &document); err != nil {
		return nil, err
	}
	if fields != nil {
		document["ioa"] = fields
	}
	return yaml.Marshal(document)
}
func productConfigAPI() managementapi.ConfigOptions {
	probes := probe.New()
	resources := resource.New()
	if _, err := resource.Define[probe.Definition](resources, probes); err != nil {
		panic(err)
	}
	for _, declare := range []func(*resource.Registry) error{scannerprobe.Declare, searchprobe.Declare, ioaprobe.Declare} {
		if err := declare(resources); err != nil {
			panic(err)
		}
	}
	resources.Freeze()
	probes.Seal()
	return managementapi.ConfigOptions{Probes: probes, Sections: productSections(false), Project: func(config *types.DistributeConfig, view *types.ConfigView) {
		client.ProjectView(config, view)
		if ext := view.Extensions[server.ConfigKey]; ext != nil && ext.Values != nil {
			value := ext.Values.Fields["url"].GetStringValue()
			if u, err := url.Parse(value); err == nil {
				u.User = nil
				ext.Values.Fields["url"] = structpb.NewStringValue(u.String())
			}
		}
	}}
}

// Restoring a masked URL keeps saved credentials only for the same endpoint.
func preserveProductURLCredentials(incoming, current cfg.Values) {
	for _, key := range []string{client.ConfigKey, server.ConfigKey} {
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
