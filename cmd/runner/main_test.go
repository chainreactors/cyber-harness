package main

import (
	"context"
	"testing"

	cfg "github.com/chainreactors/aiscan/core/config"
	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

func TestInitToolsRegistersBash(t *testing.T) {
	registry, err := initTools(context.Background(), &cfg.Option{}, telemetry.NopLogger(), eventbus.New[output.ToolDataEvent]())
	if err != nil {
		t.Fatal(err)
	}
	defer closeTools(registry)
	if _, ok := registry.GetTool("bash"); !ok {
		t.Fatal("bash tool is not registered")
	}
}
