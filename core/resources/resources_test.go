package resources

import (
	"bytes"
	"context"
	"strings"
	"testing"

	fingerresources "github.com/chainreactors/fingers/resources"
	gogopkg "github.com/chainreactors/gogo/v2/pkg"
	spraypkg "github.com/chainreactors/spray/pkg"
	"github.com/chainreactors/utils"
	zombiepkg "github.com/chainreactors/zombie/pkg"
)

func TestInitUsesAiscanEmbeddedResources(t *testing.T) {
	oldUtilsPrePort := utils.PrePort
	oldFingerPrePort := fingerresources.PrePort
	oldFingerPortData := cloneBytes(fingerresources.PortData)
	t.Cleanup(func() {
		utils.PrePort = oldUtilsPrePort
		fingerresources.PrePort = oldFingerPrePort
		fingerresources.PortData = oldFingerPortData
	})

	set, err := Init(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if set.Fingers != nil {
		t.Cleanup(func() { _ = set.Fingers.Close() })
	}
	if set.Neutron != nil {
		t.Cleanup(func() { _ = set.Neutron.Close() })
	}

	if set.Fingers == nil || set.Fingers.Count() == 0 {
		t.Fatalf("fingers engine count = 0")
	}
	if set.Neutron == nil || set.Neutron.Count() == 0 {
		t.Fatalf("neutron engine count = 0")
	}
	if len(set.GogoConfig("http")) == 0 || len(set.GogoConfig("socket")) == 0 || len(set.GogoConfig("neutron")) == 0 {
		t.Fatalf("gogo provider is missing local resources")
	}
	if len(set.SprayConfig("spray_rule")) == 0 || len(set.SprayConfig("spray_dict")) == 0 || len(set.SprayConfig("spray_common")) == 0 {
		t.Fatalf("spray provider is missing local resources")
	}
	for _, name := range []string{"zombie_common", "zombie_default", "zombie_rule", "zombie_template"} {
		data := set.ZombieConfig(name)
		if len(data) == 0 {
			t.Fatalf("zombie provider missing %s", name)
		}
		switch string(data) {
		case "[]", "{}":
			t.Fatalf("zombie provider %s returned fallback only — embedded data not generated", name)
		}
	}
	if len(set.ZombieConfig("http")) == 0 || len(set.ZombieConfig("socket")) == 0 || len(set.ZombieConfig("port")) == 0 {
		t.Fatalf("zombie provider missing shared resources")
	}
	if string(set.GogoConfig("fingerprinthub_web")) != "[]" || string(set.GogoConfig("fingerprinthub_service")) != "[]" {
		t.Fatalf("fingerprinthub fallback data should be empty JSON")
	}
	if utils.PrePort == nil || fingerresources.PrePort == nil || len(fingerresources.PortData) == 0 {
		t.Fatalf("local port preset was not installed")
	}
}

// TestPipelineDeliversAiscanBytes ensures that the bytes aiscan stages in
// gogoConfigs / sprayConfigs / zombieConfigs really arrive at the downstream
// SDK's pkg.LoadConfig — the actual call site each engine uses to read its
// templates / dictionaries / rules.
//
// This is an end-to-end-of-resource-pipeline check; it does NOT spin up engines
// or perform scans. It validates the LoadConfig contract:
//
//	(aiscan Set.XxxConfig)  ===  (sdk pkg.LoadConfig with our provider installed)
//
// Note on build tags: by default each SDK ships its own embedded fallback
// (templates.go), so a missing provider entry would silently fall through to
// SDK data — the equality check here can't distinguish that. The companion
// TestPipelineProviderActuallyDrivesLoadConfig below pins the negative case,
// and running this suite with -tags emptytemplates (which the SDK templates
// switch to no-op stubs) is the strongest harness: the suite should still
// pass because data must come from aiscan.
func TestPipelineDeliversAiscanBytes(t *testing.T) {
	oldUtilsPrePort := utils.PrePort
	oldFingerPrePort := fingerresources.PrePort
	oldFingerPortData := cloneBytes(fingerresources.PortData)
	t.Cleanup(func() {
		utils.PrePort = oldUtilsPrePort
		fingerresources.PrePort = oldFingerPrePort
		fingerresources.PortData = oldFingerPortData
		gogopkg.ResetResourceProvider()
		spraypkg.ResetResourceProvider()
		zombiepkg.ResetResourceProvider()
	})

	set, err := Init(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if set.Fingers != nil {
		t.Cleanup(func() { _ = set.Fingers.Close() })
	}
	if set.Neutron != nil {
		t.Cleanup(func() { _ = set.Neutron.Close() })
	}

	cases := []struct {
		engine          string
		install         func()
		load            func(string) []byte
		fromAiscan      func(string) []byte
		required        []string
		fallbackAllowed map[string]bool
	}{
		{
			engine:     "gogo",
			install:    func() { gogopkg.SetResourceProvider(set.GogoConfig) },
			load:       gogopkg.LoadConfig,
			fromAiscan: set.GogoConfig,
			// gogo /workspace/chainreactors/gogo/v2/pkg/load_common.go LoadConfig keys:
			// fingerprinthub_web/service are by-design empty "[]" stubs — they
			// only carry data when cyberhub remote merge is enabled, so we
			// validate byte equality but skip the non-fallback gate for them.
			required:        []string{"http", "socket", "fingerprinthub_web", "port", "extract", "workflow", "neutron"},
			fallbackAllowed: map[string]bool{"fingerprinthub_web": true, "fingerprinthub_service": true},
		},
		{
			engine:     "spray",
			install:    func() { spraypkg.SetResourceProvider(set.SprayConfig) },
			load:       spraypkg.LoadConfig,
			fromAiscan: set.SprayConfig,
			// spray /workspace/chainreactors/spray/pkg/load.go LoadConfig keys:
			required: []string{"port", "spray_rule", "spray_dict", "spray_common", "extract"},
		},
		{
			engine:     "zombie",
			install:    func() { zombiepkg.SetResourceProvider(set.ZombieConfig) },
			load:       zombiepkg.LoadConfig,
			fromAiscan: set.ZombieConfig,
			// zombie /workspace/chainreactors/zombie/pkg/loader.go LoadConfig keys:
			required: []string{"zombie_common", "zombie_default", "zombie_rule", "zombie_template", "port", "http", "socket"},
		},
	}

	for _, tc := range cases {
		tc := tc
		t.Run(tc.engine, func(t *testing.T) {
			tc.install()
			for _, key := range tc.required {
				want := tc.fromAiscan(key)
				if len(want) == 0 {
					t.Fatalf("%s: aiscan returned no bytes for key %q", tc.engine, key)
				}
				if isFallbackOnly(want) && !tc.fallbackAllowed[key] {
					t.Fatalf("%s key %q is only fallback ([]/{}) — embedded data missing", tc.engine, key)
				}
				got := tc.load(key)
				if !bytes.Equal(got, want) {
					t.Fatalf("%s key %q: SDK pkg.LoadConfig(%d bytes) != aiscan provider(%d bytes)",
						tc.engine, key, len(got), len(want))
				}
			}
		})
	}
}

// TestPipelineParsesIntoSDKStructures double-checks that aiscan-provided bytes
// for the most signal-bearing keys per engine actually parse via the same yaml
// schema each SDK expects, by re-invoking the SDK's loader functions against
// our provider. This catches the case where bytes reach LoadConfig but their
// shape differs from what the SDK's yaml.Unmarshal target expects.
func TestPipelineParsesIntoSDKStructures(t *testing.T) {
	oldUtilsPrePort := utils.PrePort
	oldFingerPrePort := fingerresources.PrePort
	oldFingerPortData := cloneBytes(fingerresources.PortData)
	t.Cleanup(func() {
		utils.PrePort = oldUtilsPrePort
		fingerresources.PrePort = oldFingerPrePort
		fingerresources.PortData = oldFingerPortData
		gogopkg.ResetResourceProvider()
		spraypkg.ResetResourceProvider()
		zombiepkg.ResetResourceProvider()
	})

	set, err := Init(context.Background(), Options{})
	if err != nil {
		t.Fatalf("Init() error = %v", err)
	}
	if set.Fingers != nil {
		t.Cleanup(func() { _ = set.Fingers.Close() })
	}
	if set.Neutron != nil {
		t.Cleanup(func() { _ = set.Neutron.Close() })
	}

	t.Run("spray_pkgLoad", func(t *testing.T) {
		spraypkg.SetResourceProvider(set.SprayConfig)
		// spraypkg.Load = LoadPorts + LoadTemplates (rules/dicts/common/extract)
		if err := spraypkg.Load(); err != nil {
			t.Fatalf("spray pkg.Load: %v", err)
		}
		if len(spraypkg.Rules) == 0 {
			t.Fatalf("spray Rules empty after Load — spray_rule not delivered")
		}
		if len(spraypkg.Dicts) == 0 {
			t.Fatalf("spray Dicts empty after Load — spray_dict not delivered")
		}
		// spray_common populates words/mask.SpecialWords; just sanity-check rules size
		if len(spraypkg.ExtractRegexps) == 0 {
			t.Fatalf("spray ExtractRegexps empty after Load — extract not delivered")
		}
	})

	t.Run("zombie_pkgLoad", func(t *testing.T) {
		zombiepkg.SetResourceProvider(set.ZombieConfig)
		// zombiepkg.Load = LoadPorts + LoadKeyword + LoadRules + LoadTemplates + LoadFingers
		if err := zombiepkg.Load(); err != nil {
			t.Fatalf("zombie pkg.Load: %v", err)
		}
		if len(zombiepkg.Keywords) == 0 {
			t.Fatalf("zombie Keywords empty after Load — zombie_common/default not delivered")
		}
		if len(zombiepkg.Rules) == 0 {
			t.Fatalf("zombie Rules empty after Load — zombie_rule not delivered")
		}
		if len(zombiepkg.TemplateMap) == 0 {
			t.Fatalf("zombie TemplateMap empty after Load — zombie_template not delivered")
		}
	})

	// gogo's pkg.Load is more involved (binds globals like FingerEngine etc),
	// so we only check the LoadConfig contract for its required keys here —
	// already covered by TestPipelineDeliversAiscanBytes above. We do however
	// sanity check a few key shapes:
	t.Run("gogo_keyShapes", func(t *testing.T) {
		gogopkg.SetResourceProvider(set.GogoConfig)
		http := gogopkg.LoadConfig("http")
		if !bytes.HasPrefix(bytes.TrimSpace(http), []byte("[")) {
			t.Fatalf("gogo http key not a JSON array: %.80s", string(http))
		}
		port := gogopkg.LoadConfig("port")
		if len(port) < 32 || strings.Contains(string(port[:32]), "{}") {
			t.Fatalf("gogo port key suspiciously empty: %d bytes", len(port))
		}
		neutron := gogopkg.LoadConfig("neutron")
		if len(neutron) < 32 {
			t.Fatalf("gogo neutron key suspiciously empty: %d bytes", len(neutron))
		}
	})
}

// TestPipelineProviderActuallyDrivesLoadConfig pins the negative case for the
// equality check in TestPipelineDeliversAiscanBytes. By default each SDK
// embeds its own copy of (some of) the same upstream files aiscan ships, so
// "aiscan bytes equal LoadConfig bytes" alone can't tell us whether the
// provider is in the call path or whether we are silently riding on the SDK
// fallback.
//
// To prove the provider actually drives LoadConfig we install a sentinel
// provider that returns deterministic dummy bytes for every key and check
// that LoadConfig returns those bytes verbatim. If the SDK ever bypasses
// SetResourceProvider, this test fails because the sentinel bytes will not
// match the embedded data.
func TestPipelineProviderActuallyDrivesLoadConfig(t *testing.T) {
	t.Cleanup(func() {
		gogopkg.ResetResourceProvider()
		spraypkg.ResetResourceProvider()
		zombiepkg.ResetResourceProvider()
	})

	sentinel := func(typ string) []byte {
		return []byte("AISCAN_SENTINEL/" + typ)
	}

	probes := []struct {
		engine  string
		key     string
		install func(func(string) []byte)
		load    func(string) []byte
	}{
		{"gogo", "http", func(p func(string) []byte) { gogopkg.SetResourceProvider(p) }, gogopkg.LoadConfig},
		{"gogo", "neutron", func(p func(string) []byte) { gogopkg.SetResourceProvider(p) }, gogopkg.LoadConfig},
		{"spray", "spray_rule", func(p func(string) []byte) { spraypkg.SetResourceProvider(p) }, spraypkg.LoadConfig},
		{"spray", "spray_dict", func(p func(string) []byte) { spraypkg.SetResourceProvider(p) }, spraypkg.LoadConfig},
		{"zombie", "zombie_default", func(p func(string) []byte) { zombiepkg.SetResourceProvider(p) }, zombiepkg.LoadConfig},
		{"zombie", "zombie_template", func(p func(string) []byte) { zombiepkg.SetResourceProvider(p) }, zombiepkg.LoadConfig},
	}

	for _, p := range probes {
		gogopkg.ResetResourceProvider()
		spraypkg.ResetResourceProvider()
		zombiepkg.ResetResourceProvider()
		p.install(sentinel)

		want := sentinel(p.key)
		got := p.load(p.key)
		if !bytes.Equal(got, want) {
			t.Fatalf("%s/%s: provider not in call path — got %q, want %q",
				p.engine, p.key, string(got), string(want))
		}
	}
}

func isFallbackOnly(b []byte) bool {
	s := strings.TrimSpace(string(b))
	return s == "[]" || s == "{}" || s == "null" || s == ""
}
