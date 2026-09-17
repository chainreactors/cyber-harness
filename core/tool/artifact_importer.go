package tool

import (
	"context"

	aoptool "github.com/chainreactors/cyber/aop/tool"
)

// ArtifactImporter normalizes scanner-native artifacts into the canonical
// persistence model.
type ArtifactImporter interface {
	ImportArtifact(context.Context, string, *aoptool.Artifact) (uint64, uint64, error)
	ArtifactTypes() []string
}
