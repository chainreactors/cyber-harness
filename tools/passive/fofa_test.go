package passive_test

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"
	"time"

	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/tools/passive"
)

func TestFofaUsesGatewayBaseURL(t *testing.T) {
	for _, prefix := range []string{"", "/", "/tenant/fofa", "/tenant/fofa/"} {
		t.Run(prefix, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				if r.URL.Path != strings.TrimRight(prefix, "/")+"/api/v1/search/all" {
					t.Errorf("query used incorrect gateway path: %s", r.URL.Path)
				}
				if r.URL.Query().Get("key") != "gateway-key" {
					t.Error("query did not use gateway credential")
				}
				fmt.Fprint(w, `{"error":false,"size":1,"results":[["192.0.2.1","443","https://example.test","example.test","Gateway asset",""]]}`)
			}))
			defer server.Close()
			engine := passive.NewUncover(passive.Config{FofaKey: "gateway-key", BaseURLs: map[string]string{"fofa": server.URL + prefix}})
			results, err := engine.Query(t.Context(), "fofa", `domain="example.test"`)
			if err != nil || len(results) != 1 || results[0].IP != "192.0.2.1" {
				t.Fatalf("gateway query: %v, %v", results, err)
			}
		})
	}
}

func TestFofaCommandCancellationIsNotEmptySuccess(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(_ http.ResponseWriter, r *http.Request) { <-r.Context().Done() }))
	defer server.Close()
	engine := passive.NewUncover(passive.Config{FofaKey: "local-secret", BaseURLs: map[string]string{"fofa": server.URL}})
	command := passive.New(engine)
	ctx, cancel := context.WithTimeout(t.Context(), 50*time.Millisecond)
	defer cancel()
	var output bytes.Buffer
	_, err := command.Run(ctx, &coretool.Execution{Args: []string{"-s", "fofa", "anything"}, Stdout: &output, Stderr: &output})
	if !errors.Is(err, context.DeadlineExceeded) || output.Len() != 0 {
		t.Fatalf("canceled query returned empty success: output=%s err=%v", output.String(), err)
	}
}
