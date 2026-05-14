//go:build generated_templates

package resources

import (
	"context"
	"testing"

	fingerresources "github.com/chainreactors/fingers/resources"
	"github.com/chainreactors/utils"
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
