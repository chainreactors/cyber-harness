// Command gen writes tools/relay/api.gen.go from cyber-relay's openapi.json.
// Run it through `go generate ./tools/relay`.
package main

import (
	"flag"
	"fmt"
	"os"
	"path/filepath"

	"github.com/chainreactors/cyber/tools/relay/internal/gen"
)

func main() {
	ref := flag.String("ref", "", "cyber-relay commit whose docs/openapi.json the client follows")
	fetch := flag.Bool("fetch", false, "download the spec at -ref before generating")
	spec := flag.String("spec", filepath.Join("testdata", "openapi.json"), "local copy of the spec (not committed)")
	out := flag.String("out", "api.gen.go", "generated file")
	flag.Parse()
	if err := run(*ref, *fetch, *spec, *out); err != nil {
		fmt.Fprintln(os.Stderr, "gen:", err)
		os.Exit(1)
	}
}

func run(ref string, fetch bool, spec, out string) error {
	if ref == "" {
		return fmt.Errorf("-ref is required")
	}
	if fetch {
		data, err := gen.Fetch(ref)
		if err != nil {
			return err
		}
		if err := os.MkdirAll(filepath.Dir(spec), 0o755); err != nil {
			return err
		}
		if err := os.WriteFile(spec, data, 0o644); err != nil {
			return err
		}
	}
	data, err := os.ReadFile(spec)
	if err != nil {
		return fmt.Errorf("%w (run with -fetch, or go generate ./tools/relay)", err)
	}
	src, err := gen.Generate(data, ref)
	if err != nil {
		return err
	}
	return os.WriteFile(out, src, 0o644)
}
