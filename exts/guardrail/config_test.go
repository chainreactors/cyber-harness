package guardrail

import (
	"encoding/json"
	"testing"

	"github.com/chainreactors/cyber/core/resource"
	cfg "github.com/chainreactors/cyber/pkg/config"
)

func TestFlatRules(t *testing.T) {
	sections := cfg.NewSections()
	resources := resource.New()
	if _, err := resource.Define[cfg.Section](resources, sections); err != nil {
		t.Fatal(err)
	}
	if err := Declare(resources); err != nil {
		t.Fatal(err)
	}
	if _, nested := sections.Defaults()[ConfigKey]["Rules"]; nested {
		t.Fatal("embedded rules acquired a configuration wrapper")
	}
	input := "guardrail:\n  provider: jev\n  mode: safe\n  level: strict\n  on_error: review\n  criteria:\n    review: operator rule\n"
	option := &cfg.Option{Sections: sections}
	if err := cfg.LoadConfigBytes([]byte(input), option); err != nil {
		t.Fatal(err)
	}
	resolved, err := sections.ResolveValues(option.Extensions, nil, nil)
	if err != nil {
		t.Fatal(err)
	}
	config, err := cfg.Get[*Config](resolved, ConfigKey)
	if err != nil {
		t.Fatal(err)
	}
	if config.Level != "strict" || config.OnError != "review" || config.Mode != ModeSafe || config.Criteria["review"] != "operator rule" {
		t.Fatalf("policy changed: %+v", config)
	}
	data, err := json.Marshal(config)
	if err != nil {
		t.Fatal(err)
	}
	var fields map[string]any
	if err := json.Unmarshal(data, &fields); err != nil {
		t.Fatal(err)
	}
	if _, nested := fields["jev"]; nested || fields["level"] != "strict" {
		t.Fatalf("display restored nested rules: %s", data)
	}
	for _, fields := range []map[string]any{
		{"level": "strict", "jev": map[string]any{"level": "permissive"}},
		{"on_error": "block", "jev": map[string]any{"on_error": "review"}},
		{"criteria": map[string]any{"review": "a"}, "jev": map[string]any{"criteria": map[string]any{"review": "b"}}},
		{"unknown": "strict"}, {"jev": map[string]any{"unknown": "strict"}}, {"on_error": "record"},
	} {
		if _, err := sections.Decode(ConfigKey, fields); err == nil {
			t.Fatalf("invalid rules accepted: %v", fields)
		}
	}
}
