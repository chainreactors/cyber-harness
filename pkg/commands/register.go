package commands

import "github.com/chainreactors/aiscan/core/capability"

func init() {
	capability.Register(capability.Descriptor{ID: "core", Kind: capability.KindTool, Group: "core"})
	RegisterFactory(Factory{
		Capability: "core",
		Build: func(deps *Deps, reg *CommandRegistry) {
			workDir := deps.WorkDir
			if workDir == "" {
				deps.Skip("core", "WorkDir")
				return
			}
			timeout := deps.BashTimeout
			if timeout <= 0 {
				timeout = 300
			}
			var readers []VirtualFileReader
			var globbers []VirtualGlobber
			if deps.SkillStore != nil {
				readers = append(readers, deps.SkillStore)
				globbers = append(globbers, deps.SkillStore)
			}
			reg.RegisterTool(NewReadTool(workDir, readers...))
			reg.RegisterTool(NewWriteTool(workDir))
			if deps.RunnerMode {
				reg.RegisterTool(NewListTool(workDir))
			}
			reg.RegisterTool(NewGlobTool(workDir, globbers...))

			bash := NewBashTool(workDir, timeout).WithScannerProxy(deps.ScannerProxy)
			bash.SetCommandNames(reg.Names)
			bash.SetCommandResolver(reg.Get)
			reg.RegisterTool(bash)

			tmuxCmd := NewTmuxCommand(bash)
			reg.Register(tmuxCmd, "core")
		},
	})
}
