package tool

import (
	"fmt"

	aop "github.com/chainreactors/cyber/aop"
	operationpb "github.com/chainreactors/cyber/aop/operation"
)

const (
	ArtifactKindService  = "service"
	ArtifactKindWeb      = "web"
	ArtifactKindWeakpass = "weakpass"
	ArtifactKindVuln     = "vuln"
)

// FromEvent extracts the canonical scanner artifact and its operation
// correlation. It is the sole envelope decoder for artifact observers; callers
// never rebuild a shadow artifact shape from individual fields.
func FromEvent(event *aop.Event) (artifact *Artifact, operationID string, found bool, err error) {
	if event == nil || event.GetExtension() == nil {
		return nil, "", false, nil
	}
	artifact = new(Artifact)
	if !event.GetExtension().MessageIs(artifact) {
		return nil, "", false, nil
	}
	if err := event.GetExtension().UnmarshalTo(artifact); err != nil {
		return nil, "", true, fmt.Errorf("decode tool artifact: %w", err)
	}
	correlation := new(operationpb.Ref)
	if correlated, findErr := aop.FindTypedExtension(event, correlation); findErr != nil {
		return nil, "", true, fmt.Errorf("decode artifact operation: %w", findErr)
	} else if correlated {
		operationID = correlation.GetCallId()
	}
	return artifact, operationID, true, nil
}
