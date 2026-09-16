//go:build full && (!record || !cgo || (!windows && !linux))

package main

import "github.com/chainreactors/cyber/core/extension"

func appendRecorderExtension(values []extension.Extension, _ applicationConfig, _ string) ([]extension.Extension, error) {
	return values, nil
}
