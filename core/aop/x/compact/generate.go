package compact

//go:generate go run github.com/atombender/go-jsonschema@v0.23.1 --only-models --tags json --struct-name-from-title --capitalization ID -p compact -o types_gen.go ../../../../web/frontend/cyber-ui/packages/agent-protocol/schema/ext/compact.schema.json
