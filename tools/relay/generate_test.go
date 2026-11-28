package relay

import (
	"bytes"
	"os"
	"path/filepath"
	"regexp"
	"testing"

	"github.com/chainreactors/cyber/tools/relay/internal/gen"
)

// The relay's spec is not committed (cyber-relay is private). When it has been
// fetched, the committed client must be exactly what it generates.
func TestGeneratedClientIsCurrent(t *testing.T) {
	spec, err := os.ReadFile(filepath.Join("testdata", "openapi.json"))
	if os.IsNotExist(err) {
		t.Skip("testdata/openapi.json is absent; run `go generate ./tools/relay` (needs read access to cyber-relay)")
	}
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile("api.gen.go")
	if err != nil {
		t.Fatal(err)
	}
	match := regexp.MustCompile(`docs/openapi\.json at ([0-9a-f]+)\.`).FindSubmatch(current)
	if match == nil {
		t.Fatal("api.gen.go does not name the spec ref it was generated from")
	}
	generated, err := gen.Generate(spec, string(match[1]))
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(generated, current) {
		t.Fatal("api.gen.go is out of date with testdata/openapi.json; run `go generate ./tools/relay`")
	}
}

// The ref in go:generate is the one api.gen.go was generated from.
func TestGenerateDirectiveMatchesClient(t *testing.T) {
	doc, err := os.ReadFile("doc.go")
	if err != nil {
		t.Fatal(err)
	}
	current, err := os.ReadFile("api.gen.go")
	if err != nil {
		t.Fatal(err)
	}
	directive := regexp.MustCompile(`(?m)^//go:generate .* -ref ([0-9a-f]{7,40})\b`).FindSubmatch(doc)
	header := regexp.MustCompile(`docs/openapi\.json at ([0-9a-f]+)\.`).FindSubmatch(current)
	if directive == nil || header == nil || string(directive[1]) != string(header[1]) {
		t.Fatal("go:generate -ref and the api.gen.go header disagree; run `go generate ./tools/relay`")
	}
}
