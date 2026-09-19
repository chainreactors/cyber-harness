//go:build !full

package aiscan

import "github.com/chainreactors/cyber/core/extension"

func browserExtension(appConfig, string) (extension.Extension, error) {
	return nil, nil
}
