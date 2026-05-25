//go:build full

package tools

import (
	"testing"

	"github.com/chainreactors/aiscan/pkg/tools/scan/engine"
	"github.com/chainreactors/sdk/gogo"
	"github.com/chainreactors/sdk/spray"
)

func TestRegisterAllRegistersKatanaInFullBuild(t *testing.T) {
	engineSet := &engine.Set{
		Gogo:  gogo.NewEngine(nil),
		Spray: spray.NewEngine(nil),
	}
	reg := buildRegistry(engineSet)

	if !reg.Has("katana") {
		t.Fatal("expected katana to be registered in full build")
	}
}

func TestRegisterAllRegistersPassiveWithUncover(t *testing.T) {
	engineSet := &engine.Set{}
	engineSet.SetupUncover(engine.ReconOptions{
		FofaEmail: "test@example.com",
		FofaKey:   "deadbeef",
	}, nil)
	reg := buildRegistry(engineSet)

	if !reg.Has("passive") {
		t.Fatal("expected passive to be registered when engineSet.Uncover is non-nil")
	}
}
