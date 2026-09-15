package client

import (
	"encoding/json"
	"fmt"
	cfg "github.com/chainreactors/cyber/core/config"
	types "github.com/chainreactors/cyber/pkg/types"
	"google.golang.org/protobuf/types/known/structpb"
	"net/url"
	"reflect"
)

// NormalizeConfig moves the IOA configuration into the extension namespace.
func NormalizeConfig(config *types.DistributeConfig) error {
	if config == nil || config.Ioa == nil {
		return nil
	}
	b, err := json.Marshal(map[string]any{"url": config.Ioa.Url, "token": config.Ioa.Token, "space": config.Ioa.Space, "node_name": config.Ioa.NodeName})
	if err != nil {
		return err
	}
	var fields map[string]any
	if err = json.Unmarshal(b, &fields); err != nil {
		return err
	}
	if config.Extensions == nil {
		config.Extensions = map[string]*structpb.Struct{}
	}
	if current := config.Extensions[ConfigKey]; current != nil {
		for key, value := range current.AsMap() {
			if old, ok := fields[key]; ok && !reflect.DeepEqual(old, value) {
				return fmt.Errorf("conflicting ioa and extensions.%s field %s", ConfigKey, key)
			}
			fields[key] = value
		}
	}
	config.Extensions[ConfigKey], err = structpb.NewStruct(fields)
	config.Ioa = nil
	return err
}
func ConfigFromExtension(config *types.DistributeConfig) *types.IOAConfig {
	if config == nil {
		return nil
	}
	raw := config.Extensions[ConfigKey]
	if raw == nil {
		return nil
	}
	b, _ := json.Marshal(raw.AsMap())
	value := &types.IOAConfig{}
	_ = json.Unmarshal(b, value)
	return value
}
func ProjectView(config *types.DistributeConfig, view *types.ConfigView) {
	value := ConfigFromExtension(config)
	if value == nil {
		return
	}
	endpoint := value.Url
	if u, err := url.Parse(endpoint); err == nil {
		u.User = nil
		endpoint = u.String()
	}
	view.Ioa = &types.IOAView{Url: endpoint, TokenConfigured: value.Token != "", NodeName: value.NodeName, Space: value.Space}
	if extension := view.Extensions[ConfigKey]; extension != nil && extension.Values != nil {
		extension.Values.Fields["url"] = structpb.NewStringValue(endpoint)
	}
}
func ValidateWire(config *types.DistributeConfig, sections *cfg.Sections) error {
	for key, fields := range cfg.ValuesFromProto(config.Extensions) {
		if _, err := sections.Decode(key, fields); err != nil {
			return err
		}
	}
	return nil
}
