//go:build full && record && cgo && (windows || linux)

package main

import (
	"path/filepath"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/pkg/exts/record"
)

func appendRecorderExtension(values []extension.Extension, config applicationConfig, workDir string) ([]extension.Extension, error) {
	if !optionalToolEnabled(config.Tools.OptionalTools, "record") {
		return values, nil
	}
	options, err := record.ReadOptions(config.Resolved)
	if err != nil {
		return nil, err
	}
	recorder, err := record.New(workDir, filepath.Join(config.DataDir, "record"), options.MaxConcurrent)
	if err != nil {
		return nil, err
	}
	return append(values, recorder), nil
}
