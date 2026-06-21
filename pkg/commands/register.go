package commands

import (
	"io"

	"github.com/chainreactors/aiscan/pkg/agent/tmux"
)

func init() {
	RegisterFactory(Factory{
		Group: "core",
		Build: func(deps *Deps, reg *CommandRegistry) {
			workDir := deps.WorkDir
			if workDir == "" {
				return
			}
			timeout := deps.BashTimeout
			if timeout <= 0 {
				timeout = 300
			}
			var readers []VirtualFileReader
			var globbers []VirtualGlobber
			if deps.SkillStore != nil {
				if r, ok := deps.SkillStore.(VirtualFileReader); ok {
					readers = append(readers, r)
				}
				if g, ok := deps.SkillStore.(VirtualGlobber); ok {
					globbers = append(globbers, g)
				}
			}
			reg.RegisterTool(NewReadTool(workDir, readers...))
			reg.RegisterTool(NewWriteTool(workDir))
			reg.RegisterTool(NewGlobTool(workDir, globbers...))

			bash := NewBashTool(workDir, timeout).WithScannerProxy(deps.ScannerProxy)
			bash.SetCommandNames(reg.Names)
			bash.Manager().SetCommands(func(name string) (tmux.Command, bool) {
				return reg.Get(name)
			})
			bash.Manager().SetExecHooks(
				func(w io.Writer) { Output.Reset(w) },
				func() { Output.Reset(nil) },
			)
			bash.Manager().SetWorkDir(workDir)
			reg.RegisterTool(bash)

			tmuxCmd := NewTmuxCommand(bash.Manager())
			reg.Register(tmuxCmd, "core")
		},
	})
}
