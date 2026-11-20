package config

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/resource"
	"github.com/chainreactors/cyber/core/types"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func TestHunterConnection(t *testing.T) {
	sections := cfg.NewSections()
	resources := resource.New()
	if _, err := resource.Define[cfg.Section](resources, sections); err != nil {
		t.Fatal(err)
	}
	if _, err := resource.Define[cfg.Connection](resources, sections.ConnectionPoint()); err != nil {
		t.Fatal(err)
	}
	if err := Declare(resources); err != nil {
		t.Fatal(err)
	}
	for _, test := range []struct {
		name, reply, message string
		ok                   bool
	}{
		{"success", `{"code":200,"message":"success","data":{"total":7}}`, "7 total", true},
		{"error", `{"code":401,"message":"invalid api-key"}`, "invalid api-key", false},
	} {
		t.Run(test.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != "/gateway/openApi/search" {
					t.Errorf("Hunter probe ignored configured base_url: %s", r.URL.Path)
				}
				if r.URL.Query().Get("api-key") != "hunter-key" {
					t.Error("connection did not use configured key")
				}
				fmt.Fprint(w, test.reply)
			}))
			defer server.Close()
			extensions, err := cfg.ValuesToProto(cfg.Values{"gateway": {"hunter": map[string]any{"key": "hunter-key", "base_url": server.URL + "/gateway"}}})
			if err != nil {
				t.Fatal(err)
			}
			checks, err := sections.TestConnection(t.Context(), ConfigKey, &types.DistributeConfig{Extensions: extensions}, nil)
			if err != nil || len(checks) != 1 {
				t.Fatalf("connection results: %v, %v", checks, err)
			}
			check := checks[0]
			if check.Name != "hunter" || check.Ok != test.ok || !strings.Contains(check.Detail+check.Error, test.message) {
				t.Fatalf("connection result: %+v", check)
			}
		})
	}
}
