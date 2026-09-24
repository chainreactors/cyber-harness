package config

import (
	"fmt"
	"maps"
	"os"
	"path/filepath"
	"reflect"
	"strconv"
	"strings"

	"gopkg.in/yaml.v3"
)

// Context supplies process inputs without imposing process globals on embedded hosts.
// Replacements substitutes staged file contents without changing discovery or precedence.
type Context struct {
	Directory    string
	Home         string
	Executable   string
	LookupEnv    func(string) (string, bool)
	Replacements map[string][]byte
}

func (c *Context) defaults() Context {
	var out Context
	if c != nil {
		out = *c
	}
	if out.Directory == "" {
		out.Directory, _ = os.Getwd()
	}
	out.Directory, _ = filepath.Abs(out.Directory)
	if out.Home == "" {
		out.Home, _ = os.UserHomeDir()
	}
	if out.Executable == "" {
		out.Executable, _ = os.Executable()
	}
	if out.LookupEnv == nil {
		out.LookupEnv = os.LookupEnv
	}
	return out
}

func (c Context) UserFile() string { return filepath.Join(c.Home, ".cyber", DefaultConfigName) }

type Layer struct {
	Path     string         `json:"path"`
	Scope    string         `json:"scope"`
	Document map[string]any `json:"-"`
}

// Snapshot owns file state separately from effective runtime values. Never serialize
// Document or Effective without Redact: both may contain credentials.
type Snapshot struct {
	Layers      []Layer           `json:"layers"`
	Target      string            `json:"target"`
	Sources     map[string]string `json:"sources"`
	Diagnostics []string          `json:"diagnostics,omitempty"`
	Document    map[string]any    `json:"-"`
	Effective   map[string]any    `json:"-"`
	Context     Context           `json:"-"`
	fileSources map[string]string
}

func regularFile(path string) bool {
	s, err := os.Stat(path)
	return err == nil && s.Mode().IsRegular()
}
func samePath(a, b string) bool {
	a, _ = filepath.Abs(a)
	b, _ = filepath.Abs(b)
	return a == b || (filepath.Separator == '\\' && strings.EqualFold(a, b))
}

// Discover overlays one configuration from the working directory on the user
// configuration. An explicit path is loaded independently of both locations.
func Discover(context *Context, explicit string) (*Snapshot, error) {
	c := context.defaults()
	s := &Snapshot{Context: c, Target: c.UserFile(), Sources: map[string]string{}, Document: map[string]any{}}
	if explicit != "" {
		if !filepath.IsAbs(explicit) {
			explicit = filepath.Join(c.Directory, explicit)
		}
		s.Target = filepath.Clean(explicit)
		s.Layers = []Layer{{Path: s.Target, Scope: "explicit"}}
		return s, nil
	}
	if regularFile(c.UserFile()) {
		s.Layers = append(s.Layers, Layer{Path: c.UserFile(), Scope: "user"})
	}
	// Prefer the established flat filename when both local layouts exist.
	for _, p := range []string{
		filepath.Join(c.Directory, DefaultConfigName),
		filepath.Join(c.Directory, ".cyber", DefaultConfigName),
	} {
		if regularFile(p) && !samePath(p, c.UserFile()) {
			s.Layers = append(s.Layers, Layer{Path: p, Scope: "project"})
			s.Target = p
			break
		}
	}
	return s, nil
}

func LoadSnapshot(context *Context, explicit string, sections *Sections) (*Snapshot, error) {
	s, err := Discover(context, explicit)
	if err != nil {
		return nil, err
	}
	// A first Web save or init may stage a new user file before it exists.
	if _, ok := s.Context.Replacements[s.Target]; ok {
		found := false
		for _, l := range s.Layers {
			found = found || samePath(l.Path, s.Target)
		}
		if !found {
			s.Layers = append(s.Layers, Layer{Path: s.Target, Scope: "user"})
		}
	}
	for i := range s.Layers {
		layer := &s.Layers[i]
		data, ok := s.Context.Replacements[layer.Path]
		if !ok {
			data, err = os.ReadFile(layer.Path)
		}
		if err != nil {
			return nil, fmt.Errorf("config file %s: %w", layer.Path, err)
		}
		var doc map[string]any
		if err = yaml.Unmarshal(data, &doc); err != nil {
			return nil, fmt.Errorf("config file %s: invalid YAML: %w", layer.Path, err)
		}
		if doc == nil {
			doc = map[string]any{}
		}
		if err = validateDocument(doc, sections); err != nil {
			return nil, fmt.Errorf("config file %s: %w", layer.Path, err)
		}
		if sections != nil {
			canonical := CloneDocument(doc)
			unknown := map[string]any{}
			if exts, ok := canonical["extensions"].(map[string]any); ok {
				for key, value := range exts {
					if !sections.Has(key) {
						unknown[key] = value
						delete(exts, key)
					}
				}
			}
			normalized, e := sections.Normalize(canonical)
			if e != nil {
				return nil, fmt.Errorf("config file %s: %w", layer.Path, e)
			}
			for key, value := range normalized {
				unknown[key] = map[string]any(value)
			}
			for _, alias := range sections.Aliases() {
				delete(doc, alias)
			}
			if len(unknown) > 0 {
				doc["extensions"] = unknown
			}
		}
		layer.Document = CloneDocument(doc)
		if err = normalizeProfileDocument(doc); err != nil {
			return nil, fmt.Errorf("config file %s: %w", layer.Path, err)
		}
		if misc, ok := doc["misc"].(map[string]any); ok {
			if value, ok := misc["data_dir"].(string); ok && value != "" && !filepath.IsAbs(value) {
				misc["data_dir"] = filepath.Join(filepath.Dir(layer.Path), value)
			}
		}
		mergeDocumentLayer(s.Document, doc, "", layer.Path, s.Sources)
	}
	if exts, ok := s.Document["extensions"].(map[string]any); ok {
		for key := range exts {
			if sections == nil || !sections.Has(key) {
				s.Diagnostics = append(s.Diagnostics, "extension unavailable in this host: "+key)
			}
		}
	}
	s.fileSources = maps.Clone(s.Sources)
	return s, nil
}

func CloneDocument(in map[string]any) map[string]any {
	out := map[string]any{}
	for k, v := range in {
		switch x := v.(type) {
		case map[string]any:
			out[k] = CloneDocument(x)
		case []any:
			a := make([]any, len(x))
			for i, e := range x {
				if m, ok := e.(map[string]any); ok {
					a[i] = CloneDocument(m)
				} else {
					a[i] = e
				}
			}
			out[k] = a
		default:
			out[k] = v
		}
	}
	return out
}

func normalizeProfileDocument(doc map[string]any) error {
	llm, _ := doc["llm"].(map[string]any)
	list, _ := llm["providers"].([]any)
	seen := map[string]bool{}
	for i, v := range list {
		entry, ok := v.(map[string]any)
		if !ok {
			return fmt.Errorf("llm.providers[%d]: expected mapping", i)
		}
		id, _ := entry["id"].(string)
		if id == "" {
			id = fmt.Sprintf("profile-%d", i+1)
			entry["id"] = id
		}
		if seen[id] {
			return fmt.Errorf("llm.providers: duplicate id %q", id)
		}
		seen[id] = true
	}
	return nil
}

func mergeDocumentLayer(dst, src map[string]any, prefix, source string, sources map[string]string) {
	for k, v := range src {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if (prefix == "llm" || strings.HasPrefix(prefix, "llm.providers.")) && v == "" && k != "active_profile" {
			continue
		}
		if nested, ok := v.(map[string]any); ok {
			target, _ := dst[k].(map[string]any)
			if target == nil {
				target = map[string]any{}
				dst[k] = target
			}
			mergeDocumentLayer(target, nested, path, source, sources)
			continue
		}
		if path == "llm.providers" {
			list, ok := v.([]any)
			if ok && len(list) > 0 {
				existing, _ := dst[k].([]any)
				for _, raw := range list {
					entry := raw.(map[string]any)
					id := entry["id"].(string)
					var target map[string]any
					for _, old := range existing {
						m := old.(map[string]any)
						if m["id"] == id {
							target = m
							break
						}
					}
					if target == nil {
						target = map[string]any{}
						existing = append(existing, target)
					}
					mergeDocumentLayer(target, entry, path+"."+id, source, sources)
				}
				dst[k] = existing
				continue
			}
		}
		dst[k] = v
		if sources != nil {
			sources[path] = source
		}
	}
}

func validateDocument(doc map[string]any, sections *Sections) error {
	return validateMapping(doc, reflect.TypeOf(Option{}), "", sections)
}

func validateMapping(doc map[string]any, t reflect.Type, prefix string, sections *Sections) error {
	fields := map[string]reflect.Type{}
	for i := 0; i < t.NumField(); i++ {
		f := t.Field(i)
		key := f.Tag.Get("config")
		if key != "" && key != "-" {
			fields[key] = f.Type
		}
	}
	for k, v := range doc {
		path := k
		if prefix != "" {
			path = prefix + "." + k
		}
		if prefix == "" && k == "extensions" {
			if _, ok := v.(map[string]any); !ok && v != nil {
				return fmt.Errorf("%s: expected mapping", path)
			}
			continue
		}
		if prefix == "" && sections != nil {
			alias := false
			for _, a := range sections.Aliases() {
				alias = alias || a == k
			}
			if alias {
				continue
			}
		}
		ft, ok := fields[k]
		if !ok {
			return fmt.Errorf("unknown configuration field %s", path)
		}
		if v == nil {
			continue
		}
		for ft.Kind() == reflect.Pointer {
			ft = ft.Elem()
		}
		switch ft.Kind() {
		case reflect.Struct:
			m, ok := v.(map[string]any)
			if !ok {
				return fmt.Errorf("%s: expected mapping", path)
			}
			if err := validateMapping(m, ft, path, sections); err != nil {
				return err
			}
		case reflect.Slice:
			a, ok := v.([]any)
			if !ok {
				return fmt.Errorf("%s: expected list", path)
			}
			if ft.Elem().Kind() == reflect.Struct {
				for i, e := range a {
					m, ok := e.(map[string]any)
					if !ok {
						return fmt.Errorf("%s[%d]: expected mapping", path, i)
					}
					if err := validateMapping(m, ft.Elem(), fmt.Sprintf("%s[%d]", path, i), sections); err != nil {
						return err
					}
				}
			}
		case reflect.String:
			if _, ok := v.(string); !ok {
				return fmt.Errorf("%s: expected string", path)
			}
		case reflect.Bool:
			if _, ok := v.(bool); !ok {
				return fmt.Errorf("%s: expected boolean", path)
			}
		case reflect.Int, reflect.Int64:
			if raw, ok := v.(string); ok && ft.Kind() == reflect.Int64 {
				if _, err := strconv.ParseInt(raw, 10, 64); err == nil {
					continue
				}
			}
			if _, ok := v.(int); !ok {
				if _, ok := v.(int64); !ok {
					return fmt.Errorf("%s: expected integer", path)
				}
			}
		}
	}
	return nil
}

// RuntimeDocument omits extensions not linked by this host, preserving them on disk.
func (s *Snapshot) RuntimeDocument(sections *Sections) map[string]any {
	doc := CloneDocument(s.Document)
	if exts, ok := doc["extensions"].(map[string]any); ok {
		for key := range exts {
			if !sections.Has(key) {
				delete(exts, key)
			}
		}
	}
	return doc
}

// FileOptions resolves profiles from file layers only, without environment or
// compiled defaults. Editors use it to avoid persisting runtime-only values.
func (s *Snapshot) FileOptions(sections *Sections) (*Option, error) {
	data, err := yaml.Marshal(s.RuntimeDocument(sections))
	if err != nil {
		return nil, err
	}
	file := *s
	file.Sources = maps.Clone(s.fileSources)
	if file.Sources == nil {
		file.Sources = maps.Clone(s.Sources)
	}
	if file.Sources == nil {
		file.Sources = map[string]string{}
	}
	option := &Option{Sections: sections, Explicit: map[string]bool{}, Snapshot: &file}
	if err := LoadConfigBytes(data, option); err != nil {
		return nil, err
	}
	if err := seedProviderProfile(option, &Option{Explicit: map[string]bool{}}); err != nil {
		return nil, err
	}
	if err := normalizeProviderOptions(option); err != nil {
		return nil, err
	}
	// File editors must not inherit compiled provider credentials either.
	for _, field := range []string{"Provider", "BaseURL", "APIKey", "Model", "LLMProxy", "MaxTokens", "ContextWindow"} {
		option.Explicit[field] = true
	}
	return option, nil
}
