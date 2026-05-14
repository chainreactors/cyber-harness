package tools

import (
	"testing"

	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	"github.com/chainreactors/aiscan/pkg/resources"
	fingerslib "github.com/chainreactors/fingers/fingers"
	sdkfingers "github.com/chainreactors/sdk/fingers"
	"github.com/chainreactors/sdk/gogo"
	"github.com/chainreactors/sdk/spray"
)

func TestRegisterAllTreatsNeutronAsOptional(t *testing.T) {
	reg := NewScannerRegistry()
	engineSet := &engine.Set{
		Gogo:  gogo.NewEngine(nil),
		Spray: spray.NewEngine(nil),
	}

	if err := RegisterAll(reg, engineSet); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	for _, name := range []string{"scan", "gogo", "spray"} {
		if !reg.Has(name) {
			t.Fatalf("expected %q to be registered", name)
		}
	}
	if reg.Has("neutron") {
		t.Fatal("neutron should not be registered without templates")
	}
}

func TestRegisterAllRegistersCyberhubWhenResourcesAvailable(t *testing.T) {
	reg := NewScannerRegistry()
	engineSet := &engine.Set{
		Resources: &resources.Set{
			FingersConfig: sdkfingers.NewConfig().WithFingers(fingerslib.Fingers{{Name: "nginx", Protocol: "http"}}),
		},
	}

	if err := RegisterAll(reg, engineSet); err != nil {
		t.Fatalf("RegisterAll() error = %v", err)
	}
	if !reg.Has("cyberhub") {
		t.Fatal("expected cyberhub to be registered")
	}
}
