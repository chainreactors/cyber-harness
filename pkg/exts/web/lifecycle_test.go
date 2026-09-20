package web_test

import (
	"context"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
	webext "github.com/chainreactors/cyber/pkg/exts/web"
	"github.com/chainreactors/cyber/pkg/profile"
	webpkg "github.com/chainreactors/cyber/pkg/web"
	"github.com/gorilla/websocket"
)

type closingProfile struct {
	profile.Profile
	closes atomic.Int32
}

func (*closingProfile) Active() bool                  { return true }
func (p *closingProfile) Close(context.Context) error { p.closes.Add(1); return nil }

func TestWebRollsBackPartiallyBuiltProfile(t *testing.T) {
	database := filepath.Join(t.TempDir(), "web.db")
	partial := new(closingProfile)
	failure := errors.New("profile construction failed")
	web := webext.New(webext.Config{Database: database, InitialProfile: func(context.Context) (profile.Profile, error) { return partial, failure }})
	if web.Service() != nil {
		t.Fatal("constructor published a service")
	}
	if _, err := os.Stat(database); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("constructor opened database: %v", err)
	}
	set := hosttest.Set(t, web)
	if err := set.Load(t.Context()); !errors.Is(err, failure) {
		t.Fatalf("load: %v", err)
	}
	if partial.closes.Load() != 1 {
		t.Fatalf("partial profile closes: %d", partial.closes.Load())
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if partial.closes.Load() != 1 {
		t.Fatal("rollback closed profile twice")
	}
}

func TestWebCloseDrainsRequestsBeforeClosingProfile(t *testing.T) {
	owned := new(closingProfile)
	web := webext.New(webext.Config{Database: filepath.Join(t.TempDir(), "web.db"), InitialProfile: func(context.Context) (profile.Profile, error) { return owned, nil }})
	entered, release, finished := make(chan struct{}), make(chan struct{}), make(chan struct{})
	plugin := routeContributor{route: webpkg.Route{Pattern: "GET /blocked", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { close(entered); <-release; w.WriteHeader(http.StatusOK) })}}
	set := hosttest.Load(t, t.Context(), web, plugin)
	var handler http.Handler
	for _, route := range web.Routes() {
		if route.Pattern == "GET /blocked" {
			handler = route.Handler
		}
	}
	if handler == nil {
		t.Fatal("missing route")
	}
	go func() {
		defer close(finished)
		handler.ServeHTTP(httptest.NewRecorder(), httptest.NewRequest("GET", "/blocked", nil))
	}()
	<-entered
	closeCtx, cancel := context.WithTimeout(t.Context(), 20*time.Millisecond)
	defer cancel()
	err := set.Close(closeCtx)
	// Always release the handler, even if the assertion fails.
	close(release)
	<-finished
	if !errors.Is(err, extension.ErrCloseIncomplete) {
		t.Fatalf("close: %v", err)
	}
	if owned.closes.Load() != 0 {
		t.Fatal("profile closed while route was running")
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if owned.closes.Load() != 1 {
		t.Fatalf("profile closes: %d", owned.closes.Load())
	}
	stale := httptest.NewRecorder()
	handler.ServeHTTP(stale, httptest.NewRequest("GET", "/blocked", nil))
	if stale.Code != http.StatusServiceUnavailable {
		t.Fatalf("revoked handler admitted work: %d", stale.Code)
	}
}

func TestWebCloseEndsIdleWebSockets(t *testing.T) {
	web := webext.New(webext.Config{Database: filepath.Join(t.TempDir(), "web.db")})
	set := hosttest.Load(t, t.Context(), web)
	handler, err := webpkg.NewHandler(web.Service().Auth(), nil, web.Routes()...)
	if err != nil {
		t.Fatal(err)
	}
	server := httptest.NewServer(handler)
	defer server.Close()
	var connections []*websocket.Conn
	for _, path := range []string{webpkg.NodeWebSocketPath, webpkg.ApplicationWebSocketPath} {
		conn, _, err := websocket.DefaultDialer.Dial("ws"+strings.TrimPrefix(server.URL, "http")+path, nil)
		if err != nil {
			t.Fatal(err)
		}
		defer conn.Close()
		connections = append(connections, conn)
	}
	ctx, cancel := context.WithTimeout(t.Context(), 2*time.Second)
	defer cancel()
	if err := set.Close(ctx); err != nil {
		t.Fatal(err)
	}
	for _, conn := range connections {
		_ = conn.SetReadDeadline(time.Now().Add(time.Second))
		if _, _, err := conn.ReadMessage(); err == nil {
			t.Fatal("websocket survived installation close")
		}
	}
}
