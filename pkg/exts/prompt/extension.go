// Package prompt installs the profile-wide prompt contribution registry.
package prompt

import (
	"context"
	"fmt"

	agentprompt "github.com/chainreactors/cyber/agent/prompt"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/resource"
)

type Extension struct {
	registry *agentprompt.Registry
}

func New() *Extension {
	return &Extension{registry: agentprompt.NewRegistry()}
}

func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil || e.registry == nil || scope == nil {
		return fmt.Errorf("prompt extension is unavailable")
	}
	if err := extension.Define[agentprompt.Contribution](scope, e.registry); err != nil {
		return err
	}
	if err := extension.Provide[agentprompt.Resolver](scope, e.registry); err != nil {
		return err
	}
	if err := extension.Add(scope, defaultContributions()...); err != nil {
		return err
	}
	return e.registry.Activate(scope.Init())
}

func (e *Extension) Close(ctx context.Context) error {
	if e == nil || e.registry == nil {
		return nil
	}
	return e.registry.Close(ctx)
}

var (
	_ extension.Extension                      = (*Extension)(nil)
	_ resource.Point[agentprompt.Contribution] = (*agentprompt.Registry)(nil)
)
