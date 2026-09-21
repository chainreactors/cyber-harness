package config

import (
	"fmt"
	"net/url"
	"path/filepath"
	"reflect"
	"strings"
)

var environmentPaths = map[string]string{
	"CYBER_PROVIDER": "llm.provider", "LLM_PROVIDER": "llm.provider",
	"CYBER_MODEL": "llm.model", "LLM_MODEL": "llm.model", "OPENAI_MODEL": "llm.model", "ANTHROPIC_MODEL": "llm.model",
	"CYBER_BASE_URL": "llm.base_url", "LLM_BASE_URL": "llm.base_url", "OPENAI_BASE_URL": "llm.base_url", "ANTHROPIC_BASE_URL": "llm.base_url",
	"CYBER_API_KEY": "llm.api_key", "LLM_API_KEY": "llm.api_key", "OPENAI_API_KEY": "llm.api_key", "ANTHROPIC_API_KEY": "llm.api_key",
	"CYBER_LLM_PROXY": "llm.proxy", "CYBER_DATA_DIR": "misc.data_dir",
	"CYBER_CYBERHUB_URL": "cyberhub.url", "CYBER_CYBERHUB_KEY": "cyberhub.key", "CYBER_CYBERHUB_MODE": "cyberhub.mode", "CYBER_PROXY": "cyberhub.proxy",
	"FOFA_KEY": "recon.fofa_key", "HUNTER_API_KEY": "recon.hunter_api_key", "RECON_PROXY": "recon.proxy", "TAVILY_API_KEY": "recon.tavily_key",
}

func sourceLookup(option *Option, lookup envLookup) envLookup {
	return func(name string) (string, bool) {
		value, ok := lookup(name)
		if ok && strings.TrimSpace(value) != "" && option.Snapshot != nil {
			if path := environmentPaths[name]; path != "" {
				option.Snapshot.Sources[path] = "env:" + name
			}
		}
		return value, ok
	}
}

func optionDocument(option *Option) map[string]any {
	var walk func(reflect.Value) map[string]any
	walk = func(value reflect.Value) map[string]any {
		out := map[string]any{}
		for i := 0; i < value.NumField(); i++ {
			field := value.Type().Field(i)
			key := field.Tag.Get("config")
			if key == "" || key == "-" {
				continue
			}
			v := value.Field(i)
			if v.Kind() == reflect.Pointer {
				if v.IsNil() {
					continue
				}
				v = v.Elem()
			}
			switch v.Kind() {
			case reflect.Struct:
				out[key] = walk(v)
			case reflect.Slice:
				list := []any{}
				for j := 0; j < v.Len(); j++ {
					item := v.Index(j)
					if item.Kind() == reflect.Struct {
						list = append(list, walk(item))
					} else {
						list = append(list, item.Interface())
					}
				}
				out[key] = list
			default:
				out[key] = v.Interface()
			}
		}
		return out
	}
	out := walk(reflect.ValueOf(option).Elem())
	extensions := map[string]any{}
	for key, value := range option.Extensions {
		extensions[key] = CloneDocument(value)
	}
	out["extensions"] = extensions
	return out
}

func finishSnapshot(option, explicit *Option) {
	s := option.Snapshot
	if s == nil {
		return
	}
	s.Effective = optionDocument(option)
	if option.Resolved != nil {
		for path, source := range option.Resolved.sources {
			if source != "file" || s.Sources[path] == "" {
				s.Sources[path] = source
			}
		}
	}
	visitOptions(option, func(field reflect.StructField, value reflect.Value, path string, _ bool) {
		if field.Tag.Get("config") == "" || field.Tag.Get("config") == "-" {
			return
		}
		if explicit.hasExplicit(field.Name) {
			s.Sources[path] = "cli"
		} else if s.Sources[path] == "" {
			s.Sources[path] = "default"
		}
	})
	if s.Sources["misc.data_dir"] == "default" {
		s.Sources["misc.data_dir"] = "discovery"
		if !samePath(option.DataDir, filepath.Dir(s.Context.UserFile())) {
			s.Diagnostics = append(s.Diagnostics, "using existing data directory: "+option.DataDir)
		}
	}
}

// Redact is used for every human/machine configuration view. Unknown extension
// bodies are hidden wholesale; a host cannot know their secret schema.
func Redact(document map[string]any, sections *Sections) map[string]any {
	out := CloneDocument(document)
	var scrub func(map[string]any)
	scrub = func(m map[string]any) {
		for key, value := range m {
			lower := strings.ToLower(key)
			if strings.Contains(lower, "key") || strings.Contains(lower, "token") || strings.Contains(lower, "password") || strings.Contains(lower, "secret") {
				// Token limits are configuration, not credentials.
				if lower != "max_tokens" && lower != "context_window" {
					if value != nil && value != "" {
						m[key] = "[redacted]"
					}
					continue
				}
			}
			switch v := value.(type) {
			case map[string]any:
				scrub(v)
			case Values:
				for _, fields := range v {
					scrub(fields)
				}
			case []any:
				for _, item := range v {
					if fields, ok := item.(map[string]any); ok {
						scrub(fields)
					}
				}
			case string:
				if u, err := url.Parse(v); err == nil && u.Host != "" {
					u.User = nil
					u.RawQuery = ""
					u.Fragment = ""
					m[key] = u.String()
				}
			}
		}
	}
	scrub(out)
	if exts, ok := out["extensions"].(map[string]any); ok {
		for key, value := range exts {
			if !sections.Has(key) {
				exts[key] = "[unavailable in this host]"
				continue
			}
			if m, ok := value.(map[string]any); ok {
				for _, secret := range sections.declarations[key].Secrets {
					parent, name := fieldParent(m, secret)
					if v, ok := parent[name]; ok && v != "" {
						parent[name] = "[redacted]"
					}
				}
			}
		}
	}
	return out
}

// Validate checks a resolved document without requiring any network credentials.
func Validate(option *Option) error {
	if err := normalizeProviderOptions(option); err != nil {
		return err
	}
	for _, entry := range option.Providers {
		if strings.TrimSpace(entry.Model) == "" {
			return fmt.Errorf("llm.providers.%s.model: required", entry.ID)
		}
	}
	if _, err := option.TrafficOptions.Normalize(); err != nil {
		return err
	}
	if option.MaxTokens < 0 || option.ContextWindow < 0 || option.Timeout < 0 {
		return fmt.Errorf("configuration limits must be nonnegative")
	}
	return nil
}
