package main

import (
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
	value, err := cfg.SharedFromOption(option)
	if err != nil {
		return nil, err
	}
	value.Search = &types.SearchConfig{TavilyKeys: tavilyKeys(recon.TavilyKey, searchKeys)}
	return value, nil
}
