//go:build !full

package main

import "github.com/chainreactors/cyber/core/extension"

func editionExtensions(applicationConfig, string) ([]extension.Extension, error) { return nil, nil }
