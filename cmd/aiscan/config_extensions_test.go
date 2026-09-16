package main

import (
	cfg "github.com/chainreactors/cyber/core/config"
	client "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	"github.com/chainreactors/cyber/pkg/types"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
	"reflect"
	"strings"
	"testing"
)

// The generated cyber.yaml is written to the flags schema, which is wider than
// the proto the settings page speaks. Reading it must not fail and saving it
// must not drop the sections the proto does not model.
func TestProductConfigKeepsSettingsTheSharedProtoDoesNotModel(t *testing.T) {
	source := []byte(productDefaultConfig())
	value, err := parseProductConfig(source)
	if err != nil {
		t.Fatalf("generated defaults rejected: %v", err)
	}
	data, err := marshalProductConfig(value, source)
	if err != nil {
		t.Fatal(err)
	}
	before, err := productOwnConfig(source)
	if err != nil {
		t.Fatal(err)
	}
	after, err := productOwnConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(before, after) {
		t.Fatalf("settings lost on save:\nbefore %#v\nafter  %#v", before, after)
	}
	for _, key := range []string{"misc", "output", "traffic", "llm"} {
		if after[key] == nil {
			t.Fatalf("section %q missing from the saved file", key)
		}
	}
	// Repeated saves must not drift: the settings page rewrites the same file
	// on every submit.
	roundTrip, err := parseProductConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	again, err := marshalProductConfig(roundTrip, data)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(data) {
		t.Fatalf("saving twice is unstable:\nfirst:\n%s\nsecond:\n%s", data, again)
	}
}

func TestGeneratedDefaultsKeepSameOriginDerivation(t *testing.T) {
	var document map[string]any
	if err := yaml.Unmarshal([]byte(productDefaultConfig()), &document); err != nil {
		t.Fatal(err)
	}
	values, err := productSections(false).Normalize(document)
	if err != nil {
		t.Fatal(err)
	}
	option := &cfg.Option{AgentOptions: cfg.AgentOptions{ServerURL: "http://web.test"}, Extensions: values}
	value, err := client.ReadOptions(option)
	if err != nil || value.URL != "http://web.test/ioa" {
		t.Fatalf("generated defaults disabled embedded IOA: %+v, %v", value, err)
	}
}

func TestProductConfigPreservesLegacyAndCanonicalPresence(t *testing.T) {
	for _, source := range []string{
		"ioa:\n  url: ''\n  space: ''\n  token: secret\n  node_name: worker\nnode:\n  name: common-node\n",
		"extensions:\n  ioa.client:\n    url: ''\n    space: ''\n    token: secret\n    node_name: worker\nnode:\n  name: common-node\n",
	} {
		value, err := parseProductConfig([]byte(source))
		if err != nil {
			t.Fatal(err)
		}
		if value.Ioa != nil {
			t.Fatal("legacy field escaped normalization")
		}
		fields := cfg.ValuesFromProto(value.Extensions)[client.ConfigKey]
		if fields["url"] != "" || fields["space"] != "" {
			t.Fatalf("zero presence lost: %#v", fields)
		}
		data, err := marshalProductConfig(value, []byte(source))
		if err != nil {
			t.Fatal(err)
		}
		if !strings.Contains(string(data), "ioa:") {
			t.Fatal("legacy YAML spelling lost")
		}
		roundTrip, err := parseProductConfig(data)
		if err != nil || !proto.Equal(value, roundTrip) {
			t.Fatalf("round trip = %v, %v\n%s", roundTrip, err, data)
		}
	}
	if _, err := parseProductConfig([]byte("ioa:\n  space: a\nextensions:\n  ioa.client:\n    space: b\n")); err == nil {
		t.Fatal("ambiguous aliases accepted")
	}
	if _, err := parseProductConfig([]byte("extensions:\n  missing:\n    enabled: true\n")); err == nil {
		t.Fatal("unregistered configuration accepted")
	}
}

func TestProductConfigMasksAndRestoresURLCredentials(t *testing.T) {
	value, err := parseProductConfig([]byte("extensions:\n  ioa.client:\n    url: https://credential@example.test/ioa\n    token: token-secret\n  ioa.server:\n    url: http://server-secret@localhost:8765\n    token: access-secret\n"))
	if err != nil {
		t.Fatal(err)
	}
	options := productConfigAPI()
	view := &types.ConfigView{Extensions: options.Sections.ProtoViews(value.Extensions)}
	options.Project(value, view)
	for _, secret := range []string{"credential", "token-secret", "server-secret", "access-secret"} {
		if strings.Contains(view.String(), secret) {
			t.Fatalf("view leaked %q", secret)
		}
	}
	if !view.GetIoa().TokenConfigured {
		t.Fatal("missing legacy secret indicator")
	}
	incoming := cfg.Values{client.ConfigKey: {"url": view.Ioa.Url}}
	preserveProductURLCredentials(incoming, cfg.ValuesFromProto(value.Extensions))
	if incoming[client.ConfigKey]["url"] != "https://credential@example.test/ioa" {
		t.Fatal("masked URL erased credential")
	}
	incoming[client.ConfigKey]["url"] = "https://different.test/ioa"
	preserveProductURLCredentials(incoming, cfg.ValuesFromProto(value.Extensions))
	if strings.Contains(incoming[client.ConfigKey]["url"].(string), "credential") {
		t.Fatal("credential copied to different host")
	}
}

func TestServingConfigDoesNotRequireClientFields(t *testing.T) {
	r := productSections(true)
	values, err := r.Normalize(map[string]any{"ioa": map[string]any{"url": "http://localhost:9000", "space": "default", "node_name": "legacy", "token": "secret"}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Decode("ioa.server", values["ioa.server"]); err != nil {
		t.Fatal(err)
	}
}

// The settings page writes a file the flags loader reads back on the next start,
// so the saved keys have to be the Option schema's snake-case spelling.
func TestProductConfigSaveStaysReadableByTheFlagsLoader(t *testing.T) {
	source := []byte("llm:\n  active_profile: main\n  providers:\n    - id: main\n      provider: openai\n      base_url: https://api.example.test/v1\n      api_key: sk-test\n      model: gpt-4o\n")
	value, err := parseProductConfig(source)
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalProductConfig(value, source)
	if err != nil {
		t.Fatal(err)
	}
	var option cfg.Option
	if err := cfg.LoadConfigBytes(data, &option); err != nil {
		t.Fatalf("saved file rejected by the flags loader: %v\n%s", err, data)
	}
	if option.ActiveProfile != "main" || len(option.Providers) != 1 {
		t.Fatalf("active profile lost: %q %#v\n%s", option.ActiveProfile, option.Providers, data)
	}
	provider := option.Providers[0]
	if provider.APIKey != "sk-test" || provider.BaseURL != "https://api.example.test/v1" || provider.Model != "gpt-4o" {
		t.Fatalf("provider lost: %+v\n%s", provider, data)
	}
}
