// Package app assembles the application surface from capabilities and
// publishes it as one.
package app

import (
	"context"
	"fmt"

	coreevents "github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	apppkg "github.com/chainreactors/cyber/pkg/app"
)

// Extension builds the App once every part it borrows exists, which is why it
// happens during load rather than in a composition root: a root cannot hold a
// Bash tool that the terminal extension only builds when it loads.
type Extension struct {
	logger      telemetry.Logger
	application *apppkg.App
}

func New(logger telemetry.Logger) *Extension { return &Extension{logger: logger} }

// App is the assembled surface. It is nil until the extension has loaded.
func (e *Extension) App() *apppkg.App {
	if e == nil {
		return nil
	}
	return e.application
}

func (e *Extension) Load(scope *extension.Scope) error {
	if e == nil {
		return fmt.Errorf("app extension is unavailable")
	}
	stream, err := extension.Use[*coreevents.Stream](scope)
	if err != nil {
		return err
	}
	application, err := apppkg.New(e.logger, stream)
	if err != nil {
		return err
	}
	e.application = application
	return extension.Provide[*apppkg.App](scope, application)
}

func (e *Extension) Close(context.Context) error { return nil }

var _ extension.Extension = (*Extension)(nil)
