package config

import (
	"context"
	"encoding/json"
	"fmt"
	"gopkg.in/yaml.v3"
	"maps"
	"os"
	"reflect"
	"sort"
	"strings"
	"sync"

	"github.com/chainreactors/cyber/core/resource"
)

// Values is configuration data, never a registry of running services.
type Values map[string]map[string]any

// Section is an inert, per-product configuration declaration.
type Section struct {
	Key         string
	Aliases     []string
	New         func() any
	Validate    func(any) error
	Secrets     []string
	AliasFields func(map[string]any) (map[string]any, error)
	Environment func(Sources) (overrides, fallbacks map[string]any, err error)
	Normalize   func(any) error
}
type Sections struct {
	mu           sync.Mutex
	declarations map[string]Section
	aliases      map[string]string
	sealed       bool
}

func NewSections() *Sections {
	return &Sections{declarations: map[string]Section{}, aliases: map[string]string{}}
}

// Add installs one atomic declaration batch.
func (r *Sections) Add(sections ...Section) (resource.Handle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return nil, fmt.Errorf("configuration declarations are sealed")
	}
	names := map[string]bool{}
	for key := range r.declarations {
		names[key] = true
	}
	for alias := range r.aliases {
		names[alias] = true
	}
	for _, section := range sections {
		if strings.TrimSpace(section.Key) == "" || section.New == nil {
			return nil, fmt.Errorf("configuration section requires key and factory")
		}
		local := map[string]bool{}
		for index, name := range append([]string{section.Key}, section.Aliases...) {
			// A root YAML alias and extensions key are different locations.
			if index > 0 && name == section.Key && !local[name] {
				local[name] = true
				continue
			}
			if strings.TrimSpace(name) == "" {
				return nil, fmt.Errorf("empty configuration name")
			}
			if names[name] {
				return nil, fmt.Errorf("duplicate configuration name %q", name)
			}
			names[name] = true
		}
	}
	registered := make([]Section, 0, len(sections))
	for _, section := range sections {
		section.Aliases = append([]string(nil), section.Aliases...)
		section.Secrets = append([]string(nil), section.Secrets...)
		r.declarations[section.Key] = section
		for _, alias := range section.Aliases {
			r.aliases[alias] = section.Key
		}
		registered = append(registered, section)
	}
	closed := false
	return resource.HandleFunc(func(context.Context) error {
		r.mu.Lock()
		defer r.mu.Unlock()
		if closed {
			return nil
		}
		closed = true
		if r.sealed {
			return nil
		}
		for _, section := range registered {
			delete(r.declarations, section.Key)
			for _, alias := range section.Aliases {
				delete(r.aliases, alias)
			}
		}
		return nil
	}), nil
}
func (r *Sections) Seal() {
	r.mu.Lock()
	r.sealed = true
	r.mu.Unlock()
}
func CloneValues(values Values) Values {
	out := Values{}
	for key, fields := range values {
		raw, err := json.Marshal(fields)
		if err != nil {
			// Keep invalid values so Decode can return their actual error. Dropping
			// them here would silently replace bad configuration with defaults.
			out[key] = maps.Clone(fields)
			continue
		}
		var copy map[string]any
		_ = json.Unmarshal(raw, &copy)
		out[key] = copy
	}
	return out
}
func mergeFields(dst, src map[string]any) {
	for key, value := range src {
		if nested, ok := value.(map[string]any); ok {
			target, _ := dst[key].(map[string]any)
			if target == nil {
				target = map[string]any{}
				dst[key] = target
			}
			mergeFields(target, nested)
		} else {
			dst[key] = value
		}
	}
}
func (r *Sections) Normalize(document map[string]any) (Values, error) {
	out := Values{}
	if raw, ok := document["extensions"]; ok {
		b, err := json.Marshal(raw)
		if err != nil {
			return nil, err
		}
		if err = json.Unmarshal(b, &out); err != nil {
			return nil, fmt.Errorf("extensions: %w", err)
		}
	}
	for alias, key := range r.aliases {
		if raw, ok := document[alias]; ok {
			b, err := json.Marshal(raw)
			if err != nil {
				return nil, err
			}
			var fields map[string]any
			if err = json.Unmarshal(b, &fields); err != nil {
				return nil, err
			}
			if transform := r.declarations[key].AliasFields; transform != nil {
				fields, err = transform(fields)
				if err != nil {
					return nil, fmt.Errorf("configuration %s: %w", alias, err)
				}
			}
			if existing, ok := out[key]; ok {
				for name, value := range fields {
					if current, present := existing[name]; present && !reflect.DeepEqual(current, value) {
						return nil, fmt.Errorf("conflicting configuration %s.%s and extensions.%s.%s", alias, name, key, name)
					}
				}
				mergeFields(fields, existing)
			}
			out[key] = fields
		}
	}
	for key := range out {
		if _, ok := r.declarations[key]; !ok {
			return nil, fmt.Errorf("unregistered extension configuration %q", key)
		}
	}
	return out, nil
}
func (r *Sections) Resolve(filename string, explicit Values) (Values, error) {
	result, err := r.ResolveSnapshot(filename, explicit, nil)
	if err != nil {
		return nil, err
	}
	return result.Values(), nil
}
func (r *Sections) ResolveSnapshot(filename string, explicit Values, lookup func(string) (string, bool)) (*Resolved, error) {
	values := Values{}
	if filename != "" {
		b, err := os.ReadFile(filename)
		if err != nil {
			return nil, err
		}
		var document map[string]any
		if err = yaml.Unmarshal(b, &document); err != nil {
			return nil, err
		}
		values, err = r.Normalize(document)
		if err != nil {
			return nil, err
		}
	}
	return r.ResolveValues(values, explicit, lookup)
}
func (r *Sections) Decode(key string, fields map[string]any) (any, error) {
	s, ok := r.declarations[key]
	if !ok {
		return nil, fmt.Errorf("unregistered extension configuration %q", key)
	}
	value := s.New()
	b, err := json.Marshal(fields)
	if err != nil {
		return nil, err
	}
	decoder := json.NewDecoder(strings.NewReader(string(b)))
	decoder.DisallowUnknownFields()
	if err = decoder.Decode(value); err != nil {
		return nil, fmt.Errorf("extension %s: %w", key, err)
	}
	if s.Normalize != nil {
		if err := s.Normalize(value); err != nil {
			return nil, fmt.Errorf("extension %s: %w", key, err)
		}
	}
	if s.Validate != nil {
		if err = s.Validate(value); err != nil {
			return nil, fmt.Errorf("extension %s: %w", key, err)
		}
	}
	return value, nil
}
func (r *Sections) Keys() []string {
	keys := make([]string, 0, len(r.declarations))
	for key := range r.declarations {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	return keys
}
func (r *Sections) Defaults() Values {
	out := Values{}
	for _, key := range r.Keys() {
		b, _ := json.Marshal(r.declarations[key].New())
		var fields map[string]any
		_ = json.Unmarshal(b, &fields)
		out[key] = fields
	}
	return out
}

// View omits secrets. Configured secret paths are metadata, not secret values.
func (r *Sections) View(values Values) (Values, map[string][]string) {
	out := CloneValues(values)
	configured := map[string][]string{}
	for key, fields := range out {
		s, ok := r.declarations[key]
		if !ok {
			delete(out, key)
			continue
		}
		for _, path := range s.Secrets {
			parent, name := fieldParent(fields, path)
			if parent == nil {
				continue
			}
			if value, ok := parent[name]; ok && value != "" && value != nil {
				configured[key] = append(configured[key], path)
			}
			delete(parent, name)
		}
	}
	return out, configured
}
func fieldParent(fields map[string]any, path string) (map[string]any, string) {
	parts := strings.Split(path, ".")
	for _, part := range parts[:len(parts)-1] {
		fields, _ = fields[part].(map[string]any)
		if fields == nil {
			return nil, ""
		}
	}
	return fields, parts[len(parts)-1]
}
func (r *Sections) Preserve(incoming, current Values) Values {
	out := CloneValues(current)
	for key, fields := range incoming {
		next := CloneValues(Values{key: fields})[key]
		for _, path := range r.declarations[key].Secrets {
			p, name := fieldParent(next, path)
			old, oldName := fieldParent(current[key], path)
			if old == nil {
				continue
			}
			if p == nil {
				p = next
				if p == nil {
					p = map[string]any{}
					next = p
				}
				parts := strings.Split(path, ".")
				for _, part := range parts[:len(parts)-1] {
					nested, _ := p[part].(map[string]any)
					if nested == nil {
						nested = map[string]any{}
						p[part] = nested
					}
					p = nested
				}
				name = parts[len(parts)-1]
			}
			if p[name] == nil || p[name] == "" {
				p[name] = old[oldName]
			}
		}
		out[key] = next
	}
	return out
}
