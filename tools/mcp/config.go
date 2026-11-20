// Package mcp adapts external MCP catalogs to harness native CLI commands.
package mcp

import (
	"bytes"
	"encoding/json"
	"fmt"
	"maps"
	"math"
	"net/url"
	"regexp"
	"slices"
	"strings"
	"time"
)

// Config selects exactly one transport. Engines and MCP servers are
// installed separately; Command is an executable, never a shell expression.
type Config struct {
	Command string            `json:"command,omitempty"`
	Args    []string          `json:"args,omitempty"`
	Env     map[string]string `json:"env,omitempty"`
	Cwd     string            `json:"cwd,omitempty"`
	URL     string            `json:"url,omitempty"`
	Headers map[string]string `json:"headers,omitempty"`
	// Omitted tools mounts everything; an explicit empty array mounts nothing.
	Tools                 []string `json:"tools"`
	StartupTimeoutSeconds int64    `json:"startupTimeoutSeconds,omitempty"`
	TimeoutSeconds        int64    `json:"timeoutSeconds,omitempty"`
}

// Parse reads a named server map with strict per-server configuration.
func Parse(data []byte) (map[string]Config, error) {
	var entries map[string]json.RawMessage
	if err := json.Unmarshal(data, &entries); err != nil {
		return nil, err
	}
	if entries == nil {
		return nil, fmt.Errorf("MCP config must be an object")
	}
	result := make(map[string]Config, len(entries))
	for name, raw := range entries {
		decoder := json.NewDecoder(bytes.NewReader(raw))
		decoder.DisallowUnknownFields()
		var config Config
		if err := decoder.Decode(&config); err != nil {
			return nil, fmt.Errorf("MCP server %s: %w", name, err)
		}
		if err := config.Validate(name); err != nil {
			return nil, err
		}
		result[name] = config
	}
	return result, nil
}

func (c Config) Clone() Config {
	c.Args = slices.Clone(c.Args)
	c.Env = maps.Clone(c.Env)
	c.Headers = maps.Clone(c.Headers)
	c.Tools = slices.Clone(c.Tools)
	return c
}

var namePattern = regexp.MustCompile(`^[a-zA-Z0-9_-]+$`)

func (c Config) Validate(name string) error {
	if !namePattern.MatchString(name) {
		return fmt.Errorf("MCP server alias must use letters, digits, '-' or '_'")
	}
	if (c.Command == "") == (c.URL == "") {
		return fmt.Errorf("MCP server %s requires exactly one of command or url", name)
	}
	if c.Command != "" {
		if len(c.Headers) != 0 {
			return fmt.Errorf("MCP server %s: headers requires url", name)
		}
		for key, value := range c.Env {
			if key == "" || strings.ContainsAny(key, "=\x00") || strings.ContainsRune(value, 0) {
				return fmt.Errorf("MCP server %s: invalid environment entry", name)
			}
		}
	} else {
		if len(c.Args) != 0 || len(c.Env) != 0 || c.Cwd != "" {
			return fmt.Errorf("MCP server %s: args, env and cwd require command", name)
		}
		u, err := url.Parse(c.URL)
		if err != nil || u.Host == "" || (u.Scheme != "http" && u.Scheme != "https") || u.User != nil || u.Fragment != "" {
			return fmt.Errorf("MCP server %s requires an absolute HTTP(S) URL without userinfo or fragment; use headers for authentication", name)
		}
	}
	for _, seconds := range []int64{c.StartupTimeoutSeconds, c.TimeoutSeconds} {
		if seconds < 0 || seconds > math.MaxInt64/int64(time.Second) {
			return fmt.Errorf("MCP server %s: timeout seconds is out of range", name)
		}
	}
	seen := make(map[string]bool)
	for _, tool := range c.Tools {
		if tool == "" {
			return fmt.Errorf("MCP server %s: empty tool in allowlist", name)
		}
		if seen[tool] {
			return fmt.Errorf("MCP server %s: repeated tool in allowlist: %s", name, tool)
		}
		seen[tool] = true
	}
	return nil
}

func timeout(seconds int64, fallback time.Duration) time.Duration {
	if seconds == 0 {
		return fallback
	}
	return time.Duration(seconds) * time.Second
}
