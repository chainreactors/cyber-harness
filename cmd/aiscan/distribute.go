package main

import (
	"strings"

	types "github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
	scannerext "github.com/chainreactors/cyber/pkg/exts/scanner"
	searchext "github.com/chainreactors/cyber/pkg/exts/search"
	"google.golang.org/protobuf/proto"
)

// DistributeFromOption projects shared harness configuration for transport and UI.
func DistributeFromOption(option *cfg.Option) (*types.DistributeConfig, error) {
	if option == nil {
		return &types.DistributeConfig{}, nil
	}
	if _, err := scannerext.ReadCyberhub(option); err != nil {
		return nil, err
	}
	recon, err := scannerext.ReadRecon(option)
	if err != nil {
		return nil, err
	}
	if _, err := scannerext.ReadScan(option); err != nil {
		return nil, err
	}
	searchKeys, err := searchext.ReadKeys(option)
	if err != nil {
		return nil, err
	}
	keys := make([]string, 0, 2)
	for _, raw := range []string{recon.TavilyKey, searchKeys} {
		if raw = strings.TrimSpace(raw); raw != "" {
			keys = append(keys, raw)
		}
	}
	extensions, err := cfg.ValuesToProto(option.Extensions)
	if err != nil {
		return nil, err
	}
	value := &types.DistributeConfig{
		Llm:        cfg.LLMFromOption(option),
		Search:     &types.SearchConfig{TavilyKeys: strings.Join(keys, ",")},
		Agent:      &types.AgentConfig{Tools: append([]string(nil), option.Tools...), Timeout: proto.Int32(int32(option.Timeout)), Heartbeat: int32(option.Heartbeat), EvalCriteria: option.EvalCriteria, EvalModel: option.EvalModel, EvalRounds: option.EvalRounds, CaptureProviderFrames: option.CaptureProviderFrames},
		Traffic:    &types.TrafficConfig{BodyStorage: option.BodyStorage, BodyMaxBytes: option.BodyMaxBytes, BodyRetentionBytes: option.BodyRetentionBytes},
		Node:       &types.NodeConfig{Id: option.NodeID, Name: option.NodeName},
		Extensions: extensions,
	}
	cfg.NormalizeLLMConfig(value.Llm)
	return value, nil
}
