package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"regexp"
	"strings"
)

var (
	flagName       = regexp.MustCompile(`^[a-zA-Z0-9_][a-zA-Z0-9_-]*$`)
	decimalInteger = regexp.MustCompile(`^-?(0|[1-9][0-9]*)$`)
)

func parameterFlag(name string) bool {
	return flagName.MatchString(name) && name != "help" && name != "json" && name != "file"
}

// namedArguments converts only supplied top-level properties. Raw input is the
// escape hatch for arbitrary JSON Schemas; this is not a schema validator.
func namedArguments(raw json.RawMessage, args []string) (json.RawMessage, error) {
	var schema struct {
		Properties map[string]json.RawMessage `json:"properties"`
		Required   []string                   `json:"required"`
	}
	if err := json.Unmarshal(raw, &schema); err != nil {
		return nil, fmt.Errorf("cannot map this input schema to flags; use --json or --file")
	}
	values := make(map[string]json.RawMessage)
	for i := 0; i < len(args); i++ {
		if !strings.HasPrefix(args[i], "--") {
			return nil, fmt.Errorf("expected --<parameter> <value>, got %q; see tool --help", args[i])
		}
		name, value, assigned := strings.Cut(strings.TrimPrefix(args[i], "--"), "=")
		property, exists := schema.Properties[name]
		if !exists || !parameterFlag(name) {
			return nil, fmt.Errorf("unknown or reserved parameter flag --%s; see tool --help or use --json/--file", name)
		}
		if _, duplicate := values[name]; duplicate {
			return nil, fmt.Errorf("repeated parameter --%s; pass arrays as one JSON value", name)
		}
		if !assigned {
			i++
			if i == len(args) || strings.HasPrefix(args[i], "--") {
				return nil, fmt.Errorf("--%s requires a value; use --%s=<value> for a value starting with --", name, name)
			}
			value = args[i]
		}
		encoded, err := parameterValue(parameterType(property), value)
		if err != nil {
			return nil, fmt.Errorf("--%s: %w", name, err)
		}
		values[name] = encoded
	}
	for _, name := range schema.Required {
		if _, present := values[name]; !present {
			return nil, fmt.Errorf("missing required parameter %q; see tool --help or use --json/--file", name)
		}
	}
	return json.Marshal(values)
}

// A single concrete type, optionally nullable, has a direct flag encoding.
// References, mixed unions and untyped branches use JSON instead of guessing.
func parameterType(raw json.RawMessage) string {
	var schema struct {
		Type  json.RawMessage   `json:"type"`
		Ref   json.RawMessage   `json:"$ref"`
		AnyOf []json.RawMessage `json:"anyOf"`
		OneOf []json.RawMessage `json:"oneOf"`
	}
	if json.Unmarshal(raw, &schema) != nil || len(schema.Ref) != 0 {
		return "JSON"
	}
	var types []string
	if len(schema.Type) != 0 {
		var single string
		if json.Unmarshal(schema.Type, &single) == nil {
			types = []string{single}
		} else if json.Unmarshal(schema.Type, &types) != nil {
			return "JSON"
		}
	} else {
		branches := schema.AnyOf
		if len(branches) == 0 {
			branches = schema.OneOf
		}
		for _, branch := range branches {
			types = append(types, parameterType(branch))
		}
	}
	kind := "null"
	for _, candidate := range types {
		if candidate == "null" {
			continue
		}
		if kind != "null" && kind != candidate {
			return "JSON"
		}
		kind = candidate
	}
	if len(types) == 0 {
		return "JSON"
	}
	switch kind {
	case "string", "integer", "number", "boolean", "object", "array", "null":
		return kind
	default:
		return "JSON"
	}
}

func parameterValue(kind, value string) (json.RawMessage, error) {
	if kind == "string" {
		return json.Marshal(value)
	}
	raw := json.RawMessage(value)
	if !json.Valid(raw) {
		return nil, fmt.Errorf("expected %s value", kind)
	}
	trimmed := bytes.TrimSpace(raw)
	valid := true
	switch kind {
	case "integer":
		valid = decimalInteger.Match(trimmed)
	case "number":
		valid = trimmed[0] == '-' || trimmed[0] >= '0' && trimmed[0] <= '9'
	case "boolean":
		valid = string(trimmed) == "true" || string(trimmed) == "false"
	case "object":
		valid = trimmed[0] == '{'
	case "array":
		valid = trimmed[0] == '['
	case "null":
		valid = string(trimmed) == "null"
	}
	if !valid {
		return nil, fmt.Errorf("expected %s value", kind)
	}
	return raw, nil
}
