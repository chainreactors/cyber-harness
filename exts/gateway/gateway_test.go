package gateway_test

import (
	"context"
	"errors"
	"fmt"
	"reflect"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/resource"
	"github.com/chainreactors/cyber/exts/gateway"
	passiveconfig "github.com/chainreactors/cyber/exts/passive/config"
	proxyext "github.com/chainreactors/cyber/exts/proxy"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func TestFlatConfigAndSecretEdits(t *testing.T) {
	sections := cfg.NewSections()
	resources := resource.New()
	if _, err := resource.Define[cfg.Section](resources, sections); err != nil {
		t.Fatal(err)
	}
	if err := gateway.Declare(resources, passiveconfig.FofaSection(), proxyext.ClashSection()); err != nil {
		t.Fatal(err)
	}
	option := &cfg.Option{Sections: sections}
	if err := cfg.LoadConfigBytes([]byte("gateway:\n  fofa:\n    key: fofa-secret\n  office:\n    kind: clash\n    url: https://example.test/token-secret\n"), option); err != nil {
		t.Fatal(err)
	}
	resolved, err := sections.ResolveValues(option.Extensions, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	option.Resolved = resolved
	values, err := gateway.Read(option)
	if err != nil || values["fofa"]["key"] != "fofa-secret" {
		t.Fatalf("read: %v %v", values, err)
	}
	values["fofa"]["key"] = "mutated"
	stored := resolved.Values()
	view, configured := sections.View(stored)
	if strings.Contains(fmt.Sprint(view), "secret") || len(configured["gateway"]) != 2 {
		t.Fatalf("unsafe view: %v metadata=%v", view, configured)
	}
	flat := map[string]any{"gateway": stored["gateway"]}
	if strings.Contains(fmt.Sprint(cfg.Redact(flat, sections)), "secret") {
		t.Fatal("flat inspection exposed credentials")
	}
	edit := sections.Preserve(cfg.Values{"gateway": {
		"office": map[string]any{"refresh": "5m"},
	}}, stored)
	if _, exists := edit["gateway"]["fofa"]; exists {
		t.Fatal("deleted instance was restored")
	}
	office := edit["gateway"]["office"].(map[string]any)
	if office["url"] != "https://example.test/token-secret" {
		t.Fatal("retained instance lost its credential")
	}
	changed := sections.Preserve(cfg.Values{"gateway": {
		"office": map[string]any{"kind": "fofa", "key": "new"},
	}}, stored)
	if _, exists := changed["gateway"]["office"].(map[string]any)["url"]; exists {
		t.Fatal("credential crossed service kinds")
	}
	if err := cfg.LoadConfigBytes([]byte("extensions:\n  gateway:\n    fofa:\n      key: fofa-secret\n"), &cfg.Option{Sections: sections}); err == nil {
		t.Fatal("obsolete extensions envelope was accepted")
	}

}

func TestBindingSelectionAndRollbackWithoutAgent(t *testing.T) {
	type config struct {
		Key string `json:"key"`
	}
	schema := cfg.Section{Key: "query", New: func() any { return &config{} }}
	var loaded, closed []string
	bind := gateway.Kind{Config: schema, Load: func(_ *extension.Scope, value any) (resource.Handle, error) {
		key := value.(*config).Key
		loaded = append(loaded, key)
		return resource.HandleFunc(func(context.Context) error { closed = append(closed, key); return nil }), nil
	}}
	values := cfg.Values{"a": {"kind": "query", "key": "one"}, "b": {"kind": "query", "key": "two"}}
	ambiguous, err := extension.New(gateway.New(values, bind))
	if err != nil {
		t.Fatal(err)
	}
	if err := ambiguous.Load(t.Context()); err == nil || len(loaded) != 0 {
		t.Fatalf("ambiguous selection changed a consumer: %v %v", err, loaded)
	}
	bind.Use = "b"
	bound, _ := extension.New(gateway.New(values, bind), extension.Func{LoadFunc: func(*extension.Scope) error { return errors.New("later load failed") }})
	if err := bound.Load(t.Context()); err == nil {
		t.Fatal("expected composition failure")
	}
	if !reflect.DeepEqual(loaded, []string{"two"}) || !reflect.DeepEqual(closed, loaded) {
		t.Fatalf("partial composition not rolled back: loaded=%v closed=%v", loaded, closed)
	}
	if err := bound.Close(t.Context()); err != nil || len(closed) != 1 {
		t.Fatalf("close was not idempotent: %v %v", closed, err)
	}
}

func TestWholeSnapshotValidatedBeforeBinding(t *testing.T) {
	for _, fields := range []cfg.Values{
		{"query": {"unexpected": true}},
		{"unknown": {"key": "secret"}},
		{"query": {"enabled": "false"}},
		{"bad.name": {"kind": "query"}},
	} {
		called := false
		set, _ := extension.New(gateway.New(fields, gateway.Kind{
			Config: cfg.Section{Key: "query", New: func() any {
				return &struct {
					Key string `json:"key"`
				}{}
			}},
			Load: func(*extension.Scope, any) (resource.Handle, error) { called = true; return nil, nil },
		}))
		if err := set.Load(t.Context()); err == nil || called {
			t.Fatalf("invalid snapshot was published: %v called=%v", err, called)
		}
	}
}
