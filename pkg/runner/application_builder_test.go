package runner

import (
	"context"
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

func TestTrafficOptionsPassThroughWithoutTranslation(t *testing.T) {
	option := &cfg.Option{TrafficOptions: cfg.TrafficOptions{
		BodyStorage: "disk", BodyMaxBytes: 1024, BodyRetentionBytes: 4096,
	}}
	direct := AppConfig(option, RuntimeFeatures{}, telemetry.NopLogger())
	merged := MergeOptionExtras(ApplicationConfig{}, option)
	if direct.Tools.TrafficStorage != option.TrafficOptions || merged.Tools.TrafficStorage != option.TrafficOptions {
		t.Fatal("traffic options were not preserved")
	}
	invalid := ApplicationConfig{}
	invalid.Tools.TrafficStorage.BodyStorage = "invalid"
	if app, err := NewApp(context.Background(), invalid); err == nil {
		if app != nil {
			app.Close()
		}
		t.Fatal("invalid storage policy did not fail before app startup")
	}
}
