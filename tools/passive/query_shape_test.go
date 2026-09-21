//go:build full

package passive_test

import (
	"bytes"
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	passivecmd "github.com/chainreactors/cyber/tools/passive"
	"github.com/chainreactors/cyber/tools/scan/engine"
	"github.com/projectdiscovery/uncover/sources"
)

func TestPassiveQueryShape(t *testing.T) {
	engine := &functionalPassiveEngine{}
	command := passivecmd.New(engine).WithLogger(telemetry.NopLogger())
	var output bytes.Buffer
	_, err := command.Run(t.Context(), &coretool.Execution{Args: []string{"-s", "fofa", `domain="example.test"`}, Stdout: &output, Stderr: &output})
	if err != nil {
		t.Fatal(err)
	}
	for _, value := range []string{`"ip":"192.0.2.10"`, `"port":"443"`, `"title":"Regression Asset"`} {
		if !strings.Contains(output.String(), value) {
			t.Fatalf("missing %s in %s", value, output.String())
		}
	}
	if len(engine.queries) != 1 || engine.queries[0] != `fofa:domain="example.test"` {
		t.Fatalf("queries: %v", engine.queries)
	}
}

type functionalPassiveEngine struct {
	queries []string
}

func (e *functionalPassiveEngine) Sources() []string { return []string{"fofa"} }

func (e *functionalPassiveEngine) QueryRaw(ctx context.Context, source, query string) ([]sources.Result, error) {
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	default:
	}
	e.queries = append(e.queries, source+":"+query)
	raw, _ := json.Marshal(engine.RawFofa{
		IP: "192.0.2.10", Port: "443", Host: "https://example.test",
		Domain: "example.test", Title: "Regression Asset", ICP: "TEST-ICP",
	})
	return []sources.Result{{
		Source: source,
		IP:     "192.0.2.10",
		Port:   443,
		Host:   "example.test",
		Raw:    raw,
	}}, nil
}
