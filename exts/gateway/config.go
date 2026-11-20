// Package gateway binds upstream APIs to existing clients in process.
// It starts no HTTP server and adds no agent tools.
package gateway

import (
	"fmt"
	"maps"
	"sort"
	"strings"

	"github.com/chainreactors/cyber/core/resource"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

const ConfigKey = "gateway"

// Declare installs one flat, named map. Each service uses an existing Section
// schema; its name is also its kind unless an explicit kind is supplied.
func Declare(resources *resource.Registry, kinds ...cfg.Section) error {
	section, err := Section(kinds...)
	if err != nil {
		return err
	}
	_, err = resource.Add(resources, section)
	return err
}

// Section validates linked kinds. With no schemas it is an inert transport
// section for configuration centers: only metadata is public, and execution
// nodes validate the concrete service schemas before loading anything.
func Section(kinds ...cfg.Section) (cfg.Section, error) {
	schemas := cfg.NewSections()
	if len(kinds) > 0 {
		if _, err := schemas.Add(kinds...); err != nil {
			return cfg.Section{}, err
		}
	}
	schemas.Seal()
	byName := make(map[string]cfg.Section, len(kinds))
	for _, kind := range kinds {
		byName[kind.Key] = kind
	}
	return cfg.Section{
		Key: ConfigKey,
		New: func() any { return new(cfg.Values) },
		Validate: func(value any) error {
			for name, fields := range *value.(*cfg.Values) {
				if len(kinds) == 0 {
					if _, err := kindName(name, fields); err != nil {
						return err
					}
					if enabled, ok := fields["enabled"]; ok {
						if _, valid := enabled.(bool); !valid {
							return fmt.Errorf("gateway.%s.enabled must be a boolean", name)
						}
					}
					continue
				}
				if _, _, err := decode(schemas, name, fields); err != nil {
					return err
				}
			}
			return nil
		},
		SecretPaths: func(fields map[string]any) []string {
			var paths []string
			for name, raw := range fields {
				entry, _ := raw.(map[string]any)
				kind, _ := kindName(name, entry)
				if schema, ok := byName[kind]; ok {
					for _, path := range schema.Secrets {
						paths = append(paths, name+"."+path)
					}
				} else if entry != nil {
					// A configuration center forwards opaque fields. Retain
					// instance names and metadata so partial edits stay safe.
					for field := range entry {
						if field != "kind" && field != "enabled" {
							paths = append(paths, name+"."+field)
						}
					}
				} else {
					paths = append(paths, name)
				}
			}
			sort.Strings(paths)
			return paths
		},
		Preserve: func(incoming, current map[string]any) map[string]any {
			for name, raw := range incoming {
				next, _ := raw.(map[string]any)
				old, _ := current[name].(map[string]any)
				if next != nil && next["kind"] == nil && old["kind"] != nil {
					next["kind"] = old["kind"]
				}
				kind, _ := kindName(name, next)
				previous, _ := kindName(name, old)
				if next == nil || old == nil || kind != previous {
					continue
				}
				if !schemas.Has(kind) {
					for field, value := range old {
						if field != "kind" && field != "enabled" && (next[field] == nil || next[field] == "") {
							next[field] = value
						}
					}
					continue
				}
				// Use the existing schema's secret preservation, per retained
				// instance. Removed instances and changed kinds stay removed.
				kept := schemas.Preserve(cfg.Values{kind: next}, cfg.Values{kind: old})
				incoming[name] = kept[kind]
			}
			return incoming
		},
	}, nil
}

func kindName(name string, fields map[string]any) (string, error) {
	if name == "" || strings.TrimSpace(name) != name || strings.ContainsAny(name, ". /\\\t\r\n") {
		return "", fmt.Errorf("invalid service name %q", name)
	}
	if raw, ok := fields["kind"]; ok {
		kind, ok := raw.(string)
		if !ok || kind == "" || strings.ContainsAny(kind, ".:/\\ \t\r\n") {
			return "", fmt.Errorf("gateway.%s.kind must name a service kind", name)
		}
		return kind, nil
	}
	return name, nil
}

func decode(schemas *cfg.Sections, name string, fields map[string]any) (string, any, error) {
	kind, err := kindName(name, fields)
	if err != nil {
		return "", nil, err
	}
	if !schemas.Has(kind) {
		return "", nil, fmt.Errorf("gateway.%s: unknown kind %q", name, kind)
	}
	fields = maps.Clone(fields)
	delete(fields, "kind")
	enabled := true
	if raw, ok := fields["enabled"]; ok {
		var valid bool
		enabled, valid = raw.(bool)
		if !valid {
			return "", nil, fmt.Errorf("gateway.%s.enabled must be a boolean", name)
		}
	}
	delete(fields, "enabled")
	if !enabled {
		return kind, nil, nil
	}
	value, err := schemas.Decode(kind, fields)
	if err != nil {
		return "", nil, fmt.Errorf("gateway.%s: %w", name, err)
	}
	return kind, value, nil
}

func Read(option *cfg.Option) (cfg.Values, error) {
	if option.Resolved != nil {
		values, err := cfg.Get[*cfg.Values](option.Resolved, ConfigKey)
		if err != nil {
			return nil, err
		}
		return cfg.CloneValues(*values), nil
	}
	values := cfg.Values{}
	for name, raw := range option.Extensions[ConfigKey] {
		entry, ok := raw.(map[string]any)
		if !ok && raw != nil {
			return nil, fmt.Errorf("gateway.%s must be an object", name)
		}
		values[name] = cfg.CloneDocument(entry)
	}
	return values, nil
}
