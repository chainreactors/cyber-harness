//go:build full

package main

import (
	"github.com/chainreactors/cyber/core/extension"
	browserext "github.com/chainreactors/cyber/pkg/exts/browser"
)

func browserExtension(config appConfig, workDir string) (extension.Extension, error) {
	if !optionalToolEnabled(config.OptionalTools, "browser") {
		return nil, nil
	}
	return browserext.New(workDir, config.PlaywrightSession)
}
