package main

import (
	"strings"

	types "github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
	scannerext "github.com/chainreactors/cyber/pkg/exts/scanner"
	searchext "github.com/chainreactors/cyber/pkg/exts/search"
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
	value, err := cfg.SharedFromOption(option)
	if err != nil {
		return nil, err
	}
	value.Search = &types.SearchConfig{TavilyKeys: strings.Join(keys, ",")}
	return value, nil
}
