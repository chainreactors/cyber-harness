// Package server installs an IOA server without owning the host's HTTP listener.
package server

import (
	"context"
	"net/http"

	"github.com/chainreactors/cyber/core/extension"
	webpkg "github.com/chainreactors/cyber/pkg/web"
	service "github.com/chainreactors/cyber/tools/ioa/server"
)

type BrowserOptions struct {
	Authenticate func(*http.Request) bool
	AuthEnabled  bool
}

type Extension struct {
	resource *service.Resource
	browser  *BrowserOptions
}

func New(config service.Config) *Extension { return &Extension{resource: service.New(config)} }

func NewBrowser(config service.Config, browser BrowserOptions) *Extension {
	return &Extension{resource: service.New(config), browser: &browser}
}

func (e *Extension) Server() *service.Server { return e.resource.Server }

func (e *Extension) Load(scope *extension.Scope) error {
	if err := e.resource.Start(scope.Init()); err != nil {
		return err
	}
	if e.browser == nil {
		return nil
	}
	handler, err := BrowserHandler(scope.Init(), e.Server(), e.browser.Authenticate, e.browser.AuthEnabled)
	if err != nil {
		return err
	}
	return extension.Add(scope, webpkg.Route{
		Pattern: "/ioa/", Handler: http.StripPrefix("/ioa", handler),
	})
}

func (e *Extension) Close(ctx context.Context) error { return e.resource.Close(ctx) }

var _ extension.Extension = (*Extension)(nil)
