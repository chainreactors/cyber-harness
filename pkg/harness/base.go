package harness

import (
	"github.com/chainreactors/cyber/agent/provider"
	toolpb "github.com/chainreactors/cyber/aop/tool"
	"github.com/chainreactors/cyber/core/egress"
	"github.com/chainreactors/cyber/core/eventbus"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	fileext "github.com/chainreactors/cyber/pkg/exts/files"
	promptext "github.com/chainreactors/cyber/pkg/exts/prompt"
	providerext "github.com/chainreactors/cyber/pkg/exts/provider"
	skillsext "github.com/chainreactors/cyber/pkg/exts/skills"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"
	tmuxext "github.com/chainreactors/cyber/pkg/exts/tmux"

	"github.com/chainreactors/cyber/tools/files"
)

type BaseConfig struct {
	// Directory is the working directory the file and terminal tools operate in.
	Directory string
	// SkillPaths and SkillExclude select the skill library's contents.
	SkillPaths   []string
	SkillExclude []string
	Terminal     terminalext.Config
	Provider     provider.StartupConfig
	Logger       telemetry.Logger
	// Egress publishes the routing endpoint the terminal borrows. A host that
	// installs a proxy passes that extension; one that routes nothing passes
	// NoEgress.
	Egress extension.Extension
}

// BaseExtensions returns the default capabilities in load order. The caller
// owns their extension.Set and chooses any additional extensions explicitly.
func BaseExtensions(c BaseConfig) ([]extension.Extension, error) {
	library, err := skillsext.NewLibrary(skillsext.LibraryConfig{
		Directory: c.Directory, Paths: c.SkillPaths, Exclude: c.SkillExclude,
	})
	if err != nil {
		return nil, err
	}
	terminal := c.Terminal
	if terminal.Directory == "" {
		terminal.Directory = c.Directory
	}
	egressProvider := c.Egress
	if egressProvider == nil {
		egressProvider = NoEgress()
	}
	// Each entry borrows only from the ones ahead of it: the registries publish
	// the executors, the egress provider publishes the routing endpoint the
	// terminal needs. Session and product consumers are installed after this base.
	return []extension.Extension{
		extension.Provided[*hooks.Registry](hooks.New()),
		extension.Provided[*events.Stream](events.New()),
		extension.Provided[*eventbus.Bus[*toolpb.Progress]](eventbus.New[*toolpb.Progress]()),
		extension.Provided[*telemetry.LoggerRef](telemetry.NewLoggerRef(c.Logger)),
		coretool.NewCommandRegistry(),
		coretool.NewToolRegistry(),
		library,
		promptext.New(),
		egressProvider,
		fileext.New(files.Config{Directory: c.Directory}),
		terminalext.New(terminal),
		tmuxext.New(),
		providerext.New(c.Provider),
	}, nil
}

// NoEgress publishes the routing endpoint of a host that routes nothing.
func NoEgress() extension.Extension {
	return extension.Provided[egress.Endpoint](egress.Disabled())
}
