package ioa

//go:generate go run github.com/atombender/go-jsonschema@v0.23.1 --only-models --tags json --struct-name-from-title --capitalization ID -p ioa -o types_gen.go ../../../../web/frontend/cyber-ui/packages/agent-protocol/schema/ext/ioa.schema.json
