//go:build !arsenal_embed

package toolchain

import crtm "github.com/chainreactors/crtm/pkg"

//go:generate go run github.com/chainreactors/crtm/cmd/crtm-bundle -config ../../cmd/cyber-audit/bundle.yaml -output . -package toolchain -metadata-only

func EmbeddedBundle() (*crtm.Bundle, error) { return nil, nil }
