//go:build full

package main

import (
	"github.com/chainreactors/cyber/core/extension"
	browserext "github.com/chainreactors/cyber/pkg/exts/browser"
)

func editionExtensions(config applicationConfig, workDir string) ([]extension.Extension, error) {
	var result []extension.Extension
	if optionalToolEnabled(config.Tools.OptionalTools, "browser") {
		browser, err := browserext.New(workDir, config.Tools.PlaywrightSession)
		if err != nil {
			return nil, err
		}
		result = append(result, browser)
	}
	return appendRecorderExtension(result, config, workDir)
}
