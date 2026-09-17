package main

import (
	cfg "github.com/chainreactors/cyber/core/config"
	"github.com/chainreactors/cyber/core/types"
	client "github.com/chainreactors/cyber/pkg/exts/ioa/client"
	"google.golang.org/protobuf/proto"
	"gopkg.in/yaml.v3"
	"reflect"
	"strings"
	"testing"
)

// The generated cyber.yaml is written to the flags schema, which is wider than
// the proto the settings page speaks. Reading it must not fail and saving it
// must not drop the sections the proto does not model.
func TestConfigKeepsSettingsTheSharedProtoDoesNotModel(t *testing.T) {
	source := []byte(defaultConfig())
	value, err := parseConfig(source)
	if err != nil {
		t.Fatalf("generated defaults rejected: %v", err)
	}
	data, err := marshalConfig(value, source)
	if err != nil {
		t.Fatal(err)
	}
	before, err := ownConfig(source)
	if err != nil {
		t.Fatal(err)
	}
	after, err := ownConfig(data)
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
	roundTrip, err := parseConfig(data)
	if err != nil {
		t.Fatal(err)
	}
	again, err := marshalConfig(roundTrip, data)
	if err != nil {
		t.Fatal(err)
	}
	if string(again) != string(data) {
		t.Fatalf("saving twice is unstable:\nfirst:\n%s\nsecond:\n%s", data, again)
	}
}

func TestGeneratedDefaultsKeepSameOriginDerivation(t *testing.T) {
	var document map[string]any
	if err := yaml.Unmarshal([]byte(defaultConfig()), &document); err != nil {
		t.Fatal(err)
	}
	values, err := defaultSections().Normalize(document)
	if err != nil {
		t.Fatal(err)
	}
	option := &cfg.Option{AgentOptions: cfg.AgentOptions{ServerURL: "http://web.test"}, Extensions: values}
	value, err := client.ReadOptions(option)
	if err != nil || value.URL != "http://web.test/ioa" {
		t.Fatalf("generated defaults disabled embedded IOA: %+v, %v", value, err)
	}
}

func TestConfigUsesCanonicalExtension(t *testing.T) {
	source := []byte("extensions:\n  ioa.client:\n    url: ''\n    space: ''\n    token: secret\n    node_name: worker\nnode:\n  name: common-node\n")
	value, err := parseConfig(source)
	if err != nil {
		t.Fatal(err)
	}
	fields := cfg.ValuesFromProto(value.Extensions)[client.ConfigKey]
	if fields["url"] != "" || fields["space"] != "" {
		t.Fatalf("zero presence lost: %#v", fields)
	}
	data, err := marshalConfig(value, source)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(string(data), "ioa.client:") {
		t.Fatal("canonical extension key was not written")
	}
	roundTrip, err := parseConfig(data)
	if err != nil || !proto.Equal(value, roundTrip) {
		t.Fatalf("round trip = %v, %v\n%s", roundTrip, err, data)
	}
	if _, err := parseConfig([]byte("ioa:\n  space: a\n")); err == nil {
		t.Fatal("removed root ioa configuration accepted")
	}
	if _, err := parseConfig([]byte("extensions:\n  missing:\n    enabled: true\n")); err == nil {
		t.Fatal("unregistered configuration accepted")
	}
}

func TestConfigMasksAndRestoresURLCredentials(t *testing.T) {
	value, err := parseConfig([]byte("extensions:\n  ioa.client:\n    url: https://credential@example.test/ioa\n    token: token-secret\n  ioa.server:\n    url: http://server-secret@localhost:8765\n    token: access-secret\n"))
	if err != nil {
		t.Fatal(err)
	}
	options := configAPI()
	view := &types.ConfigView{Extensions: options.Sections.ProtoViews(value.Extensions)}
	options.Project(value, view)
	for _, secret := range []string{"credential", "token-secret", "server-secret", "access-secret"} {
		if strings.Contains(view.String(), secret) {
			t.Fatalf("view leaked %q", secret)
		}
	}
	ioa := view.GetExtensions()[client.ConfigKey]
	if ioa == nil || len(ioa.GetConfiguredSecrets()) != 1 || ioa.GetConfiguredSecrets()[0] != "token" {
		t.Fatal("missing extension secret indicator")
	}
	incoming := cfg.Values{client.ConfigKey: {"url": ioa.GetValues().GetFields()["url"].GetStringValue()}}
	preserveURLCredentials(incoming, cfg.ValuesFromProto(value.Extensions))
	if incoming[client.ConfigKey]["url"] != "https://credential@example.test/ioa" {
		t.Fatal("masked URL erased credential")
	}
	incoming[client.ConfigKey]["url"] = "https://different.test/ioa"
	preserveURLCredentials(incoming, cfg.ValuesFromProto(value.Extensions))
	if strings.Contains(incoming[client.ConfigKey]["url"].(string), "credential") {
		t.Fatal("credential copied to different host")
	}
}

func TestServingConfigDoesNotRequireClientFields(t *testing.T) {
	r := defaultSections()
	values, err := r.Normalize(map[string]any{"extensions": map[string]any{"ioa.server": map[string]any{"url": "http://localhost:9000", "token": "secret"}}})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := r.Decode("ioa.server", values["ioa.server"]); err != nil {
		t.Fatal(err)
	}
}

// The settings page writes a file the flags loader reads back on the next start,
// so the saved keys have to be the Option schema's snake-case spelling.
func TestConfigSaveStaysReadableByTheFlagsLoader(t *testing.T) {
	source := []byte("llm:\n  active_profile: main\n  providers:\n    - id: main\n      provider: openai\n      base_url: https://api.example.test/v1\n      api_key: sk-test\n      model: gpt-4o\n")
	value, err := parseConfig(source)
	if err != nil {
		t.Fatal(err)
	}
	data, err := marshalConfig(value, source)
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
