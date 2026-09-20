//go:build !(full && record && cgo && (windows || linux))

package main

import "github.com/chainreactors/cyber/core/extension"

func recordExtension(appConfig, string) (extension.Extension, error) {
	return nil, nil
}

const recordExtensionLinked = false
