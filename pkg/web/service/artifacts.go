package service

import (
	"context"

	toolpb "github.com/chainreactors/aiscan/aop/tool"
)

// ArtifactIngestor is the server-side normalization boundary. Agent nodes send
// scanner-native records; implementations convert and persist canonical SCO.
type ArtifactIngestor interface {
	IngestArtifact(context.Context, *toolpb.Artifact) error
	NormalizeArtifact(context.Context, string, string, []byte) (uint64, uint64, error)
	SupportedArtifacts() []string
	Close() error
}
