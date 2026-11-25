package passive

import (
	"context"
	"embed"
	"fmt"
	"strings"

	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/tools/files"
	"github.com/chainreactors/cyber/tools/passive"
)

//go:embed passive.md
var docs embed.FS

type Extension struct {
	options passive.Config
	files   *files.Files
}

func New(options passive.Config) *Extension { return &Extension{options: options} }

// Load installs the passive command and its documentation only when at least
// one source is configured, so an unconfigured host exposes neither.
func (e *Extension) Load(scope *extension.Scope) error {
	logger, err := extension.Use[telemetry.Logger](scope)
	if err != nil {
		return err
	}
	backend := passive.NewUncover(e.options)
	available := backend.Sources()
	if len(available) == 0 {
		logger.Infof("%s", telemetry.StartupLine("skip", "uncover", "passive unavailable: no configured sources"))
		return nil
	}
	filesCap, err := extension.Use[*files.Files](scope)
	if err != nil {
		return err
	}
	if err = filesCap.Mount("cyber://skills/passive/", docs); err != nil {
		return err
	}
	e.files = filesCap
	logger.Infof("%s", telemetry.StartupOK("uncover", strings.Join(available, ",")))
	if err = extension.Add(scope, passive.NewCommand(backend, logger)); err != nil {
		return fmt.Errorf("register passive: %w", err)
	}
	// The read tool resolves the mount; the bundle serves skill-store reads
	// such as the playbook attached to `cyber-scan passive --ai`.
	return extension.Add(scope, skills.Bundle{ReadVirtual: func(location string) (string, bool, error) {
		if location != passive.ReferenceURI {
			return "", false, nil
		}
		raw, err := docs.ReadFile("passive.md")
		return string(raw), true, err
	}})
}
func (e *Extension) Close(ctx context.Context) error {
	if e.files == nil {
		return nil
	}
	err := e.files.Unmount(ctx, "cyber://skills/passive/")
	if err == nil {
		e.files = nil
	}
	return err
}
