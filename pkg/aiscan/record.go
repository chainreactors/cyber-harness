//go:build full && record && cgo && (windows || linux)

package aiscan

import (
	"path/filepath"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/pkg/exts/record"
)

func recordExtension(config appConfig, workDir string) (extension.Extension, error) {
	if !optionalToolEnabled(config.Tools.OptionalTools, "record") {
		return nil, nil
	}
	options, err := record.ReadOptions(config.Resolved)
	if err != nil {
		return nil, err
	}
	return record.New(workDir, filepath.Join(config.DataDir, "record"), options.MaxConcurrent)
}
