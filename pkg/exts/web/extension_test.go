package web_test

import (
	"context"
	"net/http"
	"path/filepath"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	webext "github.com/chainreactors/cyber/pkg/exts/web"
	webpkg "github.com/chainreactors/cyber/pkg/web"
)

type routeContributor struct {
	route webpkg.Route
}

func (c routeContributor) Load(scope *extension.Scope) error {
	return extension.Add(scope, c.route)
}

func TestExtensionsContributeTypedRoutes(t *testing.T) {
	routes := webext.New(webext.Config{Database: filepath.Join(t.TempDir(), "web.db")})
	plugin := routeContributor{route: webpkg.Route{
		Pattern: "GET /fixture", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	}}
	set, err := extension.New(routes, plugin)
	if err != nil {
		t.Fatal(err)
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())

	found := false
	for _, route := range routes.Routes() {
		if route.Pattern == plugin.route.Pattern {
			found = true
		}
	}
	if !found {
		t.Fatalf("plugin route is absent: %+v", routes.Routes())
	}
}

func TestRouteContributionRemovalAndDuplicates(t *testing.T) {
	routes := webext.New(webext.Config{Database: filepath.Join(t.TempDir(), "web.db")})
	set, err := extension.New(routes)
	if err != nil {
		t.Fatal(err)
	}
	defer set.Close(context.Background())
	// Routes stay hidden until the graph activates, so the snapshot is only
	// meaningful after Load -- a half-built profile is never served.
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	before := len(routes.Routes())

	route := webpkg.Route{
		Pattern: "GET /fixture", Handler: http.HandlerFunc(func(http.ResponseWriter, *http.Request) {}),
	}
	handle, err := routes.Add(route)
	if err != nil {
		t.Fatal(err)
	}
	if len(routes.Routes()) != before+1 {
		t.Fatalf("route was not published: %+v", routes.Routes())
	}
	if _, err := routes.Add(route); err == nil {
		t.Fatal("duplicate route contribution succeeded")
	}
	if err := handle.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if len(routes.Routes()) != before {
		t.Fatalf("routes after handle close = %+v", routes.Routes())
	}
}
