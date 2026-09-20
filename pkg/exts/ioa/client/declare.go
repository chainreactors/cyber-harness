package client

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/resource"
	"github.com/chainreactors/cyber/core/telemetry"
	types "github.com/chainreactors/cyber/core/types"
	hostcli "github.com/chainreactors/cyber/pkg/cli"
	cfg "github.com/chainreactors/cyber/pkg/config"
	ioatools "github.com/chainreactors/cyber/tools/ioa"
)

// Declare contributes the client resources needed before argument parsing.
func Declare(resources *resource.Registry, command hostcli.Contribution) error {
	section := Section()
	if _, err := resource.Add[hostcli.Contribution](resources,
		command,
		func(registry *hostcli.Registry) error {
			return registry.Group("agent", ConfigKey, FlagGroup())
		},
		func(registry *hostcli.Registry) error {
			return registry.Group("web", ConfigKey, FlagGroup())
		},
	); err != nil {
		return err
	}
	if _, err := resource.Add[cfg.Section](resources, section); err != nil {
		return err
	}
	_, err := resource.Add[cfg.Connection](resources, cfg.Connection{Section: ConfigKey, Test: testConnection})
	return err
}

func testConnection(ctx context.Context, in, stored *types.DistributeConfig) []*types.ConnectionCheck {
	started := time.Now()
	result := &types.ConnectionCheck{Name: ConfigKey}
	run := func() (detail string, resultErr error) {
		read := func(config *types.DistributeConfig) Options {
			if config == nil {
				return Options{}
			}
			sections := cfg.NewSections()
			_, _ = sections.Add(Section())
			value, err := sections.Decode(ConfigKey, cfg.ValuesFromProto(config.Extensions)[ConfigKey])
			if err != nil {
				return Options{}
			}
			return *value.(*Options)
		}
		value, current := read(in), read(stored)
		if strings.TrimSpace(value.URL) == "" {
			value.URL = current.URL
		}
		if strings.TrimSpace(value.Token) == "" {
			value.Token = current.Token
		}
		if value.URL == "" {
			return "", fmt.Errorf("ioa url is empty")
		}
		connectionCtx, cancel := context.WithTimeout(ctx, 20*time.Second)
		defer cancel()
		// Registration is what turns the access key into a token, and the read
		// endpoints reject an access key outright — so a server that requires one
		// can only be proven reachable by exchanging the key first.
		connection := New(ioatools.Config{URL: accessKeyURL(value.URL, value.Token), NodeName: cfg.ResolveNodeName(value.NodeName), AutoRegister: true})
		set, err := extension.New(extension.Provided(telemetry.NopLogger()), connection)
		if err != nil {
			return "", err
		}
		defer func() { resultErr = errors.Join(resultErr, set.Close(context.Background())) }()
		if err := set.Load(connectionCtx); err != nil {
			return "", err
		}
		spaces, err := connection.Service().ListSpaces(connectionCtx)
		if err != nil {
			return "", err
		}
		return fmt.Sprintf("connected - %d space(s)", len(spaces)), nil
	}
	detail, err := run()
	result.LatencyMs = time.Since(started).Milliseconds()
	result.Detail = detail
	result.Ok = err == nil
	if err != nil {
		result.Error = err.Error()
	}
	return []*types.ConnectionCheck{result}
}
