package config

import (
	"context"
	"fmt"
	"math"
	"os"
	"reflect"
	"slices"
	"sort"
	"strings"
	"sync"

	"github.com/chainreactors/cyber/core/resource"
	types "github.com/chainreactors/cyber/core/types"
	"github.com/go-viper/mapstructure/v2"
	"gopkg.in/yaml.v3"
)

// Values is configuration data, never a registry of running services.
type Values map[string]map[string]any

// Section is an inert configuration declaration.
type Section struct {
	Key      string
	New      func() any
	Validate func(any) error
	Secrets  []string
	// SecretPaths describes credentials in dynamic, named configuration maps.
	SecretPaths func(map[string]any) []string
	// Preserve handles schema-specific partial edits, before ordinary secrets.
	Preserve    func(incoming, current map[string]any) map[string]any
	Environment func(Sources) (overrides, fallbacks map[string]any, err error)
	Normalize   func(any) error
}
type Sections struct {
	mu           sync.Mutex
	declarations map[string]Section
	connections  map[string]func(context.Context, *types.DistributeConfig, *types.DistributeConfig) []*types.ConnectionCheck
	sealed       bool
}

func NewSections() *Sections {
	return &Sections{
		declarations: map[string]Section{},
		connections:  map[string]func(context.Context, *types.DistributeConfig, *types.DistributeConfig) []*types.ConnectionCheck{},
	}
}

// Add installs one atomic declaration batch.
func (r *Sections) Add(sections ...Section) (resource.Handle, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.sealed {
		return nil, fmt.Errorf("configuration declarations are sealed")
	}
	names := map[string]bool{}
	base := reflect.TypeFor[Option]()
	for i := range base.NumField() {
		if key := base.Field(i).Tag.Get("config"); key != "" && key != "-" {
			names[key] = true
		}
	}
	for key := range r.declarations {
		names[key] = true
	}
	for _, section := range sections {
		if strings.TrimSpace(section.Key) == "" || section.New == nil {
			return nil, fmt.Errorf("configuration section requires key and factory")
		}
		if names[section.Key] {
			return nil, fmt.Errorf("duplicate configuration name %q", section.Key)
		}
		names[section.Key] = true
	}
	registered := make([]Section, 0, len(sections))
	for _, section := range sections {
		section.Secrets = append([]string(nil), section.Secrets...)
		r.declarations[section.Key] = section
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
		}
		return nil
	}), nil
}
func (r *Sections) Seal() {
	r.mu.Lock()
	r.sealed = true
	r.mu.Unlock()
}

// Has only queries declarations; the host owns the declaration lifecycle.
func (r *Sections) Has(key string) bool {
	if r == nil {
		return false
	}
	r.mu.Lock()
	defer r.mu.Unlock()
	_, ok := r.declarations[key]
	return ok
}
func CloneValues(values Values) Values {
	out := make(Values, len(values))
	for key, fields := range values {
		if fields == nil {
			out[key] = nil
		} else {
			out[key] = CloneDocument(fields)
		}
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
	if err := validateMapping(document, reflect.TypeFor[Option](), "", r); err != nil {
		return nil, err
	}
	out := Values{}
	for key := range r.declarations {
		if raw, exists := document[key]; exists {
			fields, ok := raw.(map[string]any)
			if raw != nil && !ok {
				return nil, fmt.Errorf("configuration %s must be an object", key)
			}
			out[key] = CloneDocument(fields)
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
	return s.Decode(fields)
}

// Decode applies this declaration's factory, normalization and validation.
func (s Section) Decode(fields map[string]any) (any, error) {
	value := s.New()
	decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{
		TagName: "json", Result: value, ErrorUnused: true, Squash: true,
		DecodeHook: func(from, to reflect.Type, value any) (any, error) {
			kind := to.Kind()
			if kind < reflect.Int || kind > reflect.Uint64 {
				return value, nil
			}
			number := reflect.ValueOf(value)
			target := reflect.New(to).Elem()
			unsigned := kind >= reflect.Uint
			overflow := false
			switch from.Kind() {
			case reflect.Float32, reflect.Float64:
				f := number.Float()
				lower, upper := -math.Ldexp(1, to.Bits()-1), math.Ldexp(1, to.Bits()-1)
				if unsigned {
					lower, upper = 0, math.Ldexp(1, to.Bits())
				}
				overflow = math.IsNaN(f) || math.IsInf(f, 0) || math.Trunc(f) != f || f < lower || f >= upper
			case reflect.Int, reflect.Int8, reflect.Int16, reflect.Int32, reflect.Int64:
				i := number.Int()
				if unsigned {
					overflow = i < 0 || target.OverflowUint(uint64(i))
				} else {
					overflow = target.OverflowInt(i)
				}
			case reflect.Uint, reflect.Uint8, reflect.Uint16, reflect.Uint32, reflect.Uint64:
				u := number.Uint()
				if unsigned {
					overflow = target.OverflowUint(u)
				} else {
					overflow = u > (uint64(1)<<(to.Bits()-1))-1
				}
			}
			if overflow {
				return nil, fmt.Errorf("expected %s integer in range, got %v", to, value)
			}
			return value, nil
		},
	})
	if err != nil {
		return nil, err
	}
	if err := decoder.Decode(fields); err != nil {
		return nil, fmt.Errorf("extension %s: %w", s.Key, err)
	}
	if s.Normalize != nil {
		if err := s.Normalize(value); err != nil {
			return nil, fmt.Errorf("extension %s: %w", s.Key, err)
		}
	}
	if s.Validate != nil {
		if err := s.Validate(value); err != nil {
			return nil, fmt.Errorf("extension %s: %w", s.Key, err)
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
		fields := map[string]any{}
		decoder, err := mapstructure.NewDecoder(&mapstructure.DecoderConfig{TagName: "json", Result: &fields, Squash: true})
		if err != nil {
			panic(err)
		}
		if err := decoder.Decode(r.declarations[key].New()); err != nil {
			panic(fmt.Errorf("configuration %s defaults: %w", key, err))
		}
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
		for _, path := range s.secretPaths(fields) {
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
func (s Section) secretPaths(fields map[string]any) []string {
	paths := slices.Clone(s.Secrets)
	if s.SecretPaths != nil {
		paths = append(paths, s.SecretPaths(fields)...)
	}
	return paths
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
		section := r.declarations[key]
		if section.Preserve != nil {
			next = section.Preserve(next, CloneDocument(current[key]))
		}
		for _, path := range section.Secrets {
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
