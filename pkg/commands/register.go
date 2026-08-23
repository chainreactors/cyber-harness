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
				timeout = defaultTimeout
			}
			var readers []VirtualFileReader
			var globbers []VirtualGlobber
			if deps.SkillStore != nil {
				readers = append(readers, deps.SkillStore)
				globbers = append(globbers, deps.SkillStore)
			}
			reg.RegisterTool(NewReadTool(workDir, readers...).WithAudit(deps.FileAudit))
			reg.RegisterTool(NewWriteTool(workDir).WithAudit(deps.FileAudit))
			if deps.RunnerMode {
				reg.RegisterTool(NewListTool(workDir))
			}
			reg.RegisterTool(NewGlobTool(workDir, globbers...))

			bash := NewBashTool(workDir, timeout).WithScannerProxy(deps.ScannerProxy).WithScannerProxyCA(deps.ScannerProxyCA).WithEgressResolver(deps.EgressResolver).WithAudit(deps.FileAudit)
			bash.SetCommandNames(reg.Names)
			bash.SetCommandResolver(reg.Get)
			reg.RegisterTool(bash)
			bash.attachShellCommands(reg)

			tmuxCmd := NewTmuxCommand(bash)
			reg.Register(tmuxCmd, "core")
		},
	})
}
