//go:build !full

package main

import "github.com/chainreactors/cyber/core/extension"

func browserExtension(appConfig, string) (extension.Extension, error) {
	return nil, nil
}
