//go:build full && record && cgo && (windows || linux)

package main

import (
	"path/filepath"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/pkg/exts/record"
)

func recordExtension(config appConfig, workDir string) (extension.Extension, error) {
	if !optionalToolEnabled(config.OptionalTools, "record") {
		return nil, nil
	}
	options, err := record.ReadOptions(config.Resolved)
	if err != nil {
		return nil, err
	}
	return record.New(workDir, filepath.Join(config.DataDir, "record"), options.MaxConcurrent)
}

// recordExtensionLinked reports whether this build contains the record
// extension. It is set by the same tags that gate the extension itself.
const recordExtensionLinked = true
