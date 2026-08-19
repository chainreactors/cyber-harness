package runner

import (
	"testing"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/telemetry"
)

func TestAppConfigPreservesAutomaticCaptureDefault(t *testing.T) {
	option := new(cfg.Option)
	config := AppConfig(option, RuntimeFeatures{}, telemetry.NopLogger())
	if config.Tools.MitmCapture != nil {
		t.Fatal("unset MITM option must remain unset until application defaults are applied")
	}
	if !captureEnabled(config.Tools.MitmCapture) {
		t.Fatal("unset MITM option must enable capture")
	}

	disabled := false
	option.Mitm = &disabled
	config = AppConfig(option, RuntimeFeatures{}, telemetry.NopLogger())
	if config.Tools.MitmCapture == nil || *config.Tools.MitmCapture {
		t.Fatal("explicit MITM disable must be preserved")
	}
	if captureEnabled(config.Tools.MitmCapture) {
		t.Fatal("explicit MITM disable must select relay mode")
	}
}
