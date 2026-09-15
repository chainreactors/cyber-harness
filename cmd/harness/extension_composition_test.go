package harness

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"testing"

	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/pkg/cli"
	"github.com/chainreactors/cyber/pkg/commands"
	"github.com/chainreactors/cyber/pkg/toolset"
	"github.com/chainreactors/cyber/pkg/web"
	flags "github.com/jessevdk/go-flags"
)

// A feature unknown to all product option structs uses the same declaration,
// ownership and dependency rules across five independent domains.
type compositionOptions struct {
	Text  string `long:"fixture-text" config:"text" json:"text"`
	Count int    `long:"fixture-count" config:"count" json:"count"`
}

var compositionHook = hooks.Point[string, struct{}]{Kind: "fixture.executed"}

type compositionFeature struct {
	options      compositionOptions
	path         string
	hooks        *hooks.Registry
	tools        *toolset.Registry
	commands     *commands.Registry
	file         *os.File
	subscription *hooks.Subscription
	observed     string
}

func (e *compositionFeature) Name() string        { return "fixture" }
func (e *compositionFeature) Description() string { return "Fixture with explicit dependencies" }
func (e *compositionFeature) Definition() *tool.Definition {
	return tool.Def(e.Name(), e.Description(), struct{}{})
}
func (e *compositionFeature) Execute(ctx context.Context, _ string) (*tool.Result, error) {
	text := fmt.Sprintf("%s:%d", e.options.Text, e.options.Count)
	if _, err := e.file.WriteString(text); err != nil {
		return nil, err
	}
	if _, err := compositionHook.Emit(ctx, e.hooks, text); err != nil {
		return nil, err
	}
	return tool.TextResult(text), nil
}
func (e *compositionFeature) Load(scope *extension.Scope) error {
	if err := scope.Init().Err(); err != nil {
		return err
	}
	file, err := os.Create(e.path)
	if err != nil {
		return err
	}
	e.file = file
	e.subscription = compositionHook.On(e.hooks, "fixture", func(_ context.Context, text string) (struct{}, error) { e.observed = text; return struct{}{}, nil })
	if err := e.tools.Register("fixture", e); err != nil {
		return err
	}
	return e.commands.Register("fixture", "fixture", commands.Command{Name: "fixture", Run: func(ctx context.Context, _ *commands.Execution) (any, error) {
		result, err := e.tools.ExecuteTool(ctx, "fixture", "{}")
		return tool.ResultText(result), err
	}})
}
func (e *compositionFeature) Close(ctx context.Context) error {
	var err error
	if e.subscription != nil {
		err = e.subscription.Close(ctx)
	}
	if e.file != nil {
		err = errors.Join(err, e.file.Close())
		e.file = nil
	}
	return err
}

type compositionAuth struct{}

func (compositionAuth) Enabled() bool                             { return false }
func (compositionAuth) Authenticate(*http.Request) bool           { return true }
func (compositionAuth) Middleware(next http.Handler) http.Handler { return next }
func (compositionAuth) RegisterRoutes(*http.ServeMux)             {}

func TestExtensionCompositionWithoutCentralFeatureChanges(t *testing.T) {
	sections := cfg.NewSections()
	if err := sections.Register("fixture", cfg.Section{Key: "fixture", New: func() any { return &compositionOptions{Count: 9} }, Environment: func(s cfg.Sources) (map[string]any, map[string]any, error) {
		value, _ := s.LookupEnv("FIXTURE_TEXT")
		return map[string]any{"text": value}, nil, nil
	}}); err != nil {
		t.Fatal(err)
	}
	arguments := cli.New(flags.NewNamedParser("fixture-host", 0))
	if err := arguments.Group("fixture", "", "fixture", cfg.FlagGroup{Name: "Fixture", Options: &compositionOptions{}}); err != nil {
		t.Fatal(err)
	}
	if _, err := arguments.Parse([]string{"--fixture-count=0"}); err != nil {
		t.Fatal(err)
	}
	snapshot, err := sections.ResolveValues(nil, arguments.Values(), func(string) (string, bool) { return "profile", true })
	if err != nil {
		t.Fatal(err)
	}
	options, err := cfg.Get[*compositionOptions](snapshot, "fixture")
	if err != nil {
		t.Fatal(err)
	}
	hooks := hooks.New()
	tools := toolset.NewRegistry(hooks)
	commandRegistry := commands.NewRegistry(hooks)
	feature := &compositionFeature{options: *options, path: filepath.Join(t.TempDir(), "owned.txt"), hooks: hooks, tools: tools, commands: commandRegistry}
	set, err := extension.New(extension.Entry{ID: "fixture", Extension: feature}, extension.Entry{ID: "tools", DependsOn: []string{"fixture"}, Extension: tools}, extension.Entry{ID: "commands", DependsOn: []string{"tools"}, Extension: commandRegistry})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := set.Close(context.Background()); err != nil {
			t.Error(err)
		}
	})
	handler, err := web.NewHandler(compositionAuth{}, nil, web.Route{Source: "fixture", Pattern: "GET /fixture", Handler: http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		result, err := commandRegistry.Execute(r.Context(), "fixture", &commands.Execution{})
		if err != nil {
			http.Error(w, err.Error(), http.StatusServiceUnavailable)
			return
		}
		fmt.Fprint(w, result)
	})})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(feature.path); !os.IsNotExist(err) {
		t.Fatal("declaration opened resources")
	}
	if len(tools.ToolDefinitions()) != 0 {
		t.Fatal("constructor published a tool")
	}
	if err := set.Load(t.Context()); err != nil {
		t.Fatal(err)
	}
	recorder := httptest.NewRecorder()
	handler.ServeHTTP(recorder, httptest.NewRequest("GET", "/fixture", nil))
	body, _ := io.ReadAll(recorder.Result().Body)
	if recorder.Code != http.StatusOK || string(body) != "profile:0" || feature.observed != "profile:0" {
		t.Fatalf("cross-domain dispatch: %d %s %s", recorder.Code, body, feature.observed)
	}
	if err := set.Close(t.Context()); err != nil {
		t.Fatal(err)
	}
	if feature.file != nil {
		t.Fatal("resource did not close")
	}
	after := httptest.NewRecorder()
	handler.ServeHTTP(after, httptest.NewRequest("GET", "/fixture", nil))
	if after.Code != http.StatusServiceUnavailable {
		t.Fatal("closed graph admitted a request")
	}
}
