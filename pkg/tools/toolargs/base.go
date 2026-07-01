package toolargs

import (
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/telemetry"
)

type Base struct {
	Logger  telemetry.Logger
	Proxy   string
	WorkDir string
	DataBus *eventbus.Bus[output.ToolDataEvent]
}

func (b *Base) SetWorkDir(dir string) { b.WorkDir = dir }
func (b *Base) SetProxy(proxy string) { b.Proxy = proxy }

func (b *Base) InitLogger(logger telemetry.Logger) {
	if logger != nil {
		b.Logger = logger
	} else {
		b.Logger = telemetry.NopLogger()
	}
}

func (b *Base) EmitData(tool, kind, target string, data any) {
	if b.DataBus == nil {
		return
	}
	b.DataBus.Emit(output.ToolDataEvent{
		Tool:      tool,
		Kind:      kind,
		Target:    target,
		Data:      data,
		Timestamp: time.Now(),
	})
}
