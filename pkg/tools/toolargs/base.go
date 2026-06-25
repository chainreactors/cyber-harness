package toolargs

import "github.com/chainreactors/aiscan/pkg/telemetry"

type Base struct {
	Logger  telemetry.Logger
	Proxy   string
	WorkDir string
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
