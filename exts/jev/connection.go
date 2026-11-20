package jev

import (
	"context"
	"maps"
	"os"
	"slices"
	"strings"
	"time"

	jevapi "github.com/chainreactors/cyber/agent/provider/jev"
	"github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"google.golang.org/protobuf/types/known/structpb"
)

// ProjectView exposes only presence of an environment credential. The settings
// document remains unchanged and the key never enters its editable values.
func ProjectView(view *types.ConfigView) {
	if strings.TrimSpace(os.Getenv("TYPESAFE_API_KEY")) == "" {
		return
	}
	if view.Extensions == nil {
		view.Extensions = make(map[string]*types.ExtensionView)
	}
	entry := view.Extensions[ConfigKey]
	if entry == nil {
		entry = &types.ExtensionView{Values: &structpb.Struct{Fields: map[string]*structpb.Value{}}}
		view.Extensions[ConfigKey] = entry
	}
	if !slices.Contains(entry.ConfiguredSecrets, "api_key") {
		entry.ConfiguredSecrets = append(entry.ConfiguredSecrets, "api_key")
	}
}

// testConnection judges an inert local-read description. It never executes a
// tool, and uses judge directly so a fallback cannot masquerade as API success.
func testConnection(ctx context.Context, incoming, stored *types.DistributeConfig) []*types.ConnectionCheck {
	started := time.Now()
	check := &types.ConnectionCheck{Name: "jev"}
	defer func() { check.LatencyMs = time.Since(started).Milliseconds() }()
	values := maps.Clone(cfg.ValuesFromProto(stored.GetExtensions())[ConfigKey])
	if values == nil {
		values = map[string]any{}
	}
	for key, value := range cfg.ValuesFromProto(incoming.GetExtensions())[ConfigKey] {
		if key == "api_key" && (value == nil || value == "") {
			continue
		}
		values[key] = value
	}
	decoded, err := configSection.Decode(values)
	if err != nil {
		check.Error = "Invalid JEV configuration"
		return []*types.ConnectionCheck{check}
	}
	config := decoded.(*Config)
	if strings.TrimSpace(config.APIKey) == "" {
		config.APIKey = os.Getenv("TYPESAFE_API_KEY")
	}
	if strings.TrimSpace(config.APIKey) == "" {
		check.Error = "JEV API key is not configured"
		return []*types.ConnectionCheck{check}
	}
	duration, _ := time.ParseDuration(config.Timeout)
	ctx, cancel := context.WithTimeout(ctx, duration)
	defer cancel()
	client := jevapi.New(config.APIKey, config.Model, duration)
	client.Endpoint = strings.TrimRight(config.BaseURL, "/") + "/v1/systemone"
	defer client.Close()
	claim := choiceClaim("Classify this inert connection-test description: read public README.md locally, no writes or network. Do not execute anything.", map[string]string{"record": "Reading a public local document.", "review": "Uncertain effects.", "block": "Destructive effects."})
	out, err := client.Evaluate(ctx, map[string]jevapi.Claim{"action": claim})
	choice := ""
	if err == nil {
		choice, err = out.Choice("action", claim)
	}
	if err != nil {
		check.Error = err.Error()
		return []*types.ConnectionCheck{check}
	}
	check.Ok = true
	check.Detail = "JEV " + strings.ToUpper(choice) + "; judgment only, no tool executed"
	return []*types.ConnectionCheck{check}
}
