package main

import (
	cfg "github.com/chainreactors/aiscan/core/config"
	client "github.com/chainreactors/aiscan/pkg/exts/ioa/client"
	ioaprobe "github.com/chainreactors/aiscan/pkg/exts/ioa/client/probe"
	server "github.com/chainreactors/aiscan/pkg/exts/ioa/server"
	scannerprobe "github.com/chainreactors/aiscan/pkg/exts/scanner/probe"
	searchprobe "github.com/chainreactors/aiscan/pkg/exts/search/probe"
	"github.com/chainreactors/aiscan/pkg/probe"
	types "github.com/chainreactors/aiscan/pkg/types"
	managementapi "github.com/chainreactors/aiscan/pkg/web/api"
	"google.golang.org/protobuf/proto"
	"google.golang.org/protobuf/types/known/structpb"
	"gopkg.in/yaml.v3"
	"net/url"
)

func normalizeProductConfig(config *types.DistributeConfig) error {
	return client.NormalizeWire(config)
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
	for name, check := range map[string]probe.Check{"cyberhub": scannerprobe.Cyberhub, "recon": scannerprobe.Recon, "search": searchprobe.Check} {
		if err := probes.Register(name, name, check); err != nil {
			panic(err)
		}
	}
	if err := probes.Register("ioa.client", "ioa", ioaprobe.Check); err != nil {
		panic(err)
	}
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
