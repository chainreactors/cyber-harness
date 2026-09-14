// Package server installs an IOA server without owning the host's HTTP listener.
package server

import (
	"context"
	"github.com/chainreactors/aiscan/core/extension"
	service "github.com/chainreactors/aiscan/tools/ioa/server"
)

type Extension struct{ resource *service.Resource }

func New(config service.Config) *Extension             { return &Extension{resource: service.New(config)} }
func (e *Extension) Server() *service.Server           { return e.resource.Server }
func (e *Extension) Load(scope *extension.Scope) error { return e.resource.Start(scope.Init()) }
func (e *Extension) Close(ctx context.Context) error   { return e.resource.Close(ctx) }

var _ extension.Extension = (*Extension)(nil)
