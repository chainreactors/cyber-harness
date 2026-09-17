// Package base owns the extensions every Cyber host needs, whatever else it
// selects, and returns them in the order they must load.
//
// Composition still belongs to the executable: base hands back a slice, and the
// host owns the extension.Set, the order of anything it adds, and the decision
// to add it at all. Nothing is threaded back out -- New returns extensions and
// an error, and nothing else. If it ever needed to return a hook registry, a
// Bash tool or an application, a capability would be missing.
package base

import (
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/core/egress"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/pkg/commands"
	appext "github.com/chainreactors/cyber/pkg/exts/app"
	fileext "github.com/chainreactors/cyber/pkg/exts/files"
	providerext "github.com/chainreactors/cyber/pkg/exts/provider"
	skillsext "github.com/chainreactors/cyber/pkg/exts/skills"
	terminalext "github.com/chainreactors/cyber/pkg/exts/terminal"
	tmuxext "github.com/chainreactors/cyber/pkg/exts/tmux"
	"github.com/chainreactors/cyber/pkg/toolset"
	"github.com/chainreactors/cyber/tools/files"
)

type Config struct {
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

// New returns the core extensions in load order.
func New(c Config) ([]extension.Extension, error) {
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
	// terminal needs, and the application is assembled before anything reads it.
	return []extension.Extension{
		extension.Provided[*hooks.Registry](hooks.New()),
		extension.Provided[*events.Stream](events.New()),
		commands.NewRegistry(),
		toolset.NewRegistry(),
		library,
		egressProvider,
		fileext.New(files.Config{Directory: c.Directory}),
		terminalext.New(terminal),
		tmuxext.New(),
		appext.New(c.Logger),
		providerext.New(c.Provider, c.Logger),
	}, nil
}

// NoEgress publishes the routing endpoint of a host that routes nothing.
func NoEgress() extension.Extension {
	return extension.Provided[egress.Endpoint](egress.Disabled())
}
