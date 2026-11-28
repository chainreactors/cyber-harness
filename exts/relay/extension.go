package relay

import (
	"context"
	"embed"
	"fmt"
	"io/fs"
	"strings"

	"github.com/chainreactors/cyber/agent/skills"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/resource"
	"github.com/chainreactors/cyber/core/telemetry"
	"github.com/chainreactors/cyber/exts/gateway"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/tools/files"
	relaytool "github.com/chainreactors/cyber/tools/relay"
)

//go:embed skill/*.md
var content embed.FS

const mount = "cyber://skills/relay/"

type Extension struct {
	client *relaytool.Client
	files  *files.Files
}

func New() *Extension { return &Extension{} }

// Gateway binds gateway.relay. It creates the client only; Load decides what
// the agent sees from whether a client exists.
func (e *Extension) Gateway() gateway.Kind {
	return gateway.Kind{Config: Section(), Load: func(_ *extension.Scope, value any) (resource.Handle, error) {
		if value == nil {
			return nil, nil
		}
		api := value.(*cfg.API)
		client, err := relaytool.New(relaytool.Config{BaseURL: api.BaseURL, Key: api.Key})
		if err != nil {
			return nil, err
		}
		e.client = client
		return nil, nil
	}}
}

// Load installs the relay command, its documents and the cyber-relay skill
// only when gateway.relay configured a client.
func (e *Extension) Load(scope *extension.Scope) error {
	logger, err := extension.Use[telemetry.Logger](scope)
	if err != nil {
		return err
	}
	if e.client == nil {
		logger.Infof("%s", telemetry.StartupLine("skip", "relay", "relay unavailable: gateway.relay is not configured"))
		return nil
	}
	docs, err := fs.Sub(content, "skill")
	if err != nil {
		return err
	}
	bundle, err := skillBundle(docs)
	if err != nil {
		return err
	}
	filesCap, err := extension.Use[*files.Files](scope)
	if err != nil {
		return err
	}
	if err := filesCap.Mount(mount, docs); err != nil {
		return err
	}
	e.files = filesCap
	if err := extension.Add(scope, relaytool.NewCommand(e.client)); err != nil {
		return fmt.Errorf("register relay: %w", err)
	}
	if err := extension.Add(scope, bundle); err != nil {
		return err
	}
	logger.Infof("%s", telemetry.StartupOK("relay", "gateway.relay"))
	return nil
}

func skillBundle(docs fs.FS) (skills.Bundle, error) {
	raw, err := fs.ReadFile(docs, "SKILL.md")
	if err != nil {
		return skills.Bundle{}, err
	}
	fm, _ := skills.ParseFrontmatter(string(raw))
	if fm.Name == "" || fm.Description == "" {
		return skills.Bundle{}, fmt.Errorf("relay SKILL.md requires name and description")
	}
	return skills.Bundle{
		Skills: []skills.Skill{{
			Name: fm.Name, Description: fm.Description, Internal: fm.Internal, Source: skills.SourceBundle,
			Location: relaytool.DescriptionPath, BaseDir: strings.TrimSuffix(mount, "/"),
		}},
		ReadVirtual: func(location string) (string, bool, error) {
			name, ok := strings.CutPrefix(location, mount)
			if !ok {
				return "", false, nil
			}
			data, err := fs.ReadFile(docs, name)
			if err != nil {
				return "", false, nil
			}
			return string(data), true, nil
		},
	}, nil
}

func (e *Extension) Close(ctx context.Context) error {
	if e.files == nil {
		return nil
	}
	err := e.files.Unmount(ctx, mount)
	if err == nil {
		e.files = nil
	}
	return err
}

var _ extension.Extension = (*Extension)(nil)
