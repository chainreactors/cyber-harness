package config

import (
	"encoding/json"
	"fmt"
)

// Sources preserves presence at each precedence layer. LookupEnv is supplied
// by the host; extension configuration never reads process state implicitly.
type Sources struct {
	File, CLI map[string]any
	LookupEnv func(string) (string, bool)
}

// Resolved is a configuration snapshot, never a container of runtime services.
// It belongs to one configuration evaluation and is not updated in place.
type Resolved struct {
	values Values
	typed  map[string]any
}

func (r *Resolved) Values() Values {
	if r == nil {
		return Values{}
	}
	return CloneValues(r.values)
}

// Get returns an owned configuration copy. T is the value type returned by the
// section's New function (usually a pointer to its feature's Options).
func Get[T any](r *Resolved, key string) (T, error) {
	var out T
	if r == nil {
		return out, fmt.Errorf("configuration is not resolved")
	}
	value, ok := r.typed[key]
	if !ok {
		return out, fmt.Errorf("configuration %q is not declared", key)
	}
	if _, ok := value.(T); !ok {
		return out, fmt.Errorf("configuration %q has type %T", key, value)
	}
	b, err := json.Marshal(value)
	if err != nil {
		return out, err
	}
	err = json.Unmarshal(b, &out)
	return out, err
}

func cloneFields(fields map[string]any) map[string]any {
	if fields == nil {
		return map[string]any{}
	}
	return CloneValues(Values{"value": fields})["value"]
}

// ResolveValues applies CLI > application env > file > protocol env > defaults.
// Every declared section is validated, including sections omitted by the user.
func (r *Sections) ResolveValues(file, cli Values, lookup func(string) (string, bool)) (*Resolved, error) {
	r.Seal()
	result := &Resolved{values: Values{}, typed: map[string]any{}}
	for _, input := range []Values{file, cli} {
		for key := range input {
			if _, ok := r.declarations[key]; !ok {
				return nil, fmt.Errorf("unregistered extension configuration %q", key)
			}
		}
	}
	if lookup == nil {
		lookup = func(string) (string, bool) { return "", false }
	}
	for _, key := range r.Keys() {
		section := r.declarations[key]
		var overrides, fallbacks map[string]any
		if section.Environment != nil {
			var err error
			overrides, fallbacks, err = section.Environment(Sources{File: cloneFields(file[key]), CLI: cloneFields(cli[key]), LookupEnv: lookup})
			if err != nil {
				return nil, fmt.Errorf("configuration %s: %w", key, err)
			}
		}
		fields := cloneFields(fallbacks)
		mergeFields(fields, cloneFields(file[key]))
		mergeFields(fields, cloneFields(overrides))
		mergeFields(fields, cloneFields(cli[key]))
		value, err := r.Decode(key, fields)
		if err != nil {
			return nil, err
		}
		result.typed[key] = value
		// Keep explicit presence separately from the decoded defaults.
		result.values[key] = fields
	}
	return result, nil
}
