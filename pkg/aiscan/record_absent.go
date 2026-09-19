//go:build !(full && record && cgo && (windows || linux))

package aiscan

import "github.com/chainreactors/cyber/core/extension"

func recordExtension(appConfig, string) (extension.Extension, error) {
	return nil, nil
}
