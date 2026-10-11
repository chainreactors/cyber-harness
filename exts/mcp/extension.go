// Package mcp exposes external MCP servers as native commands through bash.
package mcp

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"sync"

	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
	mcptools "github.com/chainreactors/cyber/tools/mcp"
	"mvdan.cc/sh/v3/interp"
	"mvdan.cc/sh/v3/syntax"
)

type Config struct {
	MCPServers map[string]mcptools.ServerConfig `json:"mcpServers"`
}

type Extension struct {
	mu          sync.Mutex
	config      Config
	connections []*mcptools.Connection
	loaded      bool
	closed      bool
}

func New(config Config) *Extension {
	copy := Config{MCPServers: make(map[string]mcptools.ServerConfig, len(config.MCPServers))}
	for name, server := range config.MCPServers {
		copy.MCPServers[name] = server.Clone()
	}
	return &Extension{config: copy}
}

func (e *Extension) Load(scope *extension.Scope) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.loaded || e.closed {
		return fmt.Errorf("MCP extension already loaded or closed")
	}
	e.loaded = true
	names := make([]string, 0, len(e.config.MCPServers))
	for name := range e.config.MCPServers {
		names = append(names, name)
	}
	sort.Strings(names)
	// Validate the whole configuration before starting any server.
	for _, name := range names {
		if err := e.config.MCPServers[name].Validate(name); err != nil {
			return err
		}
		if interp.IsBuiltin(name) || syntax.IsKeyword(name) {
			return fmt.Errorf("MCP server alias %q conflicts with a bash builtin or keyword; choose another alias", name)
		}
	}
	var commands []coretool.Command
	for _, name := range names {
		connection, err := mcptools.New(name, e.config.MCPServers[name])
		if err != nil {
			return err
		}
		// Retain ownership before starting: rollback includes partial Start.
		e.connections = append(e.connections, connection)
		if err := connection.Start(scope.Init(), scope.Lifetime()); err != nil {
			return err
		}
		discovered, err := connection.Discover(scope.Init())
		if err != nil {
			return err
		}
		commands = append(commands, discovered.Command())
	}
	if len(commands) == 0 {
		return nil
	}
	// One atomic contribution; a failed load never exposes half a catalog.
	return extension.Add[coretool.Command](scope, commands...)
}

func (e *Extension) Close(ctx context.Context) error {
	e.mu.Lock()
	defer e.mu.Unlock()
	if e.closed {
		return nil
	}
	var errs []error
	for len(e.connections) > 0 {
		index := len(e.connections) - 1
		if err := e.connections[index].Close(ctx); err != nil {
			errs = append(errs, err)
			if errors.Is(err, extension.ErrCloseIncomplete) {
				return errors.Join(errs...)
			}
		}
		e.connections = e.connections[:index]
	}
	e.closed = true
	return errors.Join(errs...)
}

var _ extension.Extension = (*Extension)(nil)
