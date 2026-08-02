package tool

import (
	"encoding/json"
	"fmt"

	aop "github.com/chainreactors/aiscan/aop"
	"github.com/invopop/jsonschema"
)

// SchemaOf generates a JSON Schema (as map[string]any) from a Go struct.
func SchemaOf(proto any) map[string]any {
	r := &jsonschema.Reflector{
		DoNotReference: true,
	}
	schema := r.Reflect(proto)

	data, err := json.Marshal(schema)
	if err != nil {
		return map[string]any{"type": "object"}
	}

	var m map[string]any
	if err := json.Unmarshal(data, &m); err != nil {
		return map[string]any{"type": "object"}
	}

	delete(m, "$schema")
	delete(m, "$id")
	delete(m, "$defs")
	return m
}

// Def builds a complete Definition from a name,
// description, and an args struct prototype.
func Def(name, description string, argsProto any) *aop.ToolDefinition {
	schema, _ := aop.JSONValue(SchemaOf(argsProto))
	return &aop.ToolDefinition{
		Type:        "function",
		Name:        name,
		Description: description,
		InputSchema: schema,
	}
}

// ParseArgs unmarshals the raw JSON arguments string into a typed struct.
func ParseArgs[T any](arguments string) (T, error) {
	var args T
	if err := json.Unmarshal([]byte(arguments), &args); err != nil {
		return args, fmt.Errorf("invalid arguments: %w", err)
	}
	return args, nil
}
