//go:build !(js && wasm)

// This package only builds for GOOS=js GOARCH=wasm. The stub keeps `go build
// ./...` and `go vet ./...` green on native targets; build the real thing with
// `make agent-wasm`.
package main

import (
	"fmt"
	"os"
)

func main() {
	fmt.Fprintln(os.Stderr, "cmd/wasm builds only with GOOS=js GOARCH=wasm; run: make agent-wasm")
	os.Exit(1)
}
