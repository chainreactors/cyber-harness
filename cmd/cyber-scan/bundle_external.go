//go:build !arsenal_embed

package main

import crtm "github.com/chainreactors/crtm/pkg"

//go:generate go run github.com/chainreactors/crtm/cmd/crtm-bundle -config bundle.yaml -output . -package main -metadata-only

func EmbeddedBundle() (*crtm.Bundle, error) { return nil, nil }
