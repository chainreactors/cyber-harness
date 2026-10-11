package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"time"
)

// Catalog is an immutable initial snapshot of the allowlisted upstream tools.
// Tool names and their complete declarations remain upstream-owned values.
type Catalog struct {
	connection *Connection
	tools      []declaration
	byName     map[string]int
}

// Discover follows all pages before filtering. No business tool is called.
// Raw declarations avoid the SDK's typed schema dropping unknown keywords.
func (c *Connection) Discover(ctx context.Context) (*Catalog, error) {
	ctx, cancel := context.WithTimeout(ctx, timeout(c.config.StartupTimeoutSeconds, 30*time.Second))
	defer cancel()
	var tools []declaration
	seenTools, seenCursors := map[string]bool{}, map[string]bool{}
	cursor := ""
	for {
		params := map[string]string{}
		if cursor != "" {
			params["cursor"] = cursor
		}
		raw, err := c.request(ctx, "tools/list", params)
		if err != nil {
			return nil, err
		}
		var page struct {
			Tools      []json.RawMessage `json:"tools"`
			NextCursor string            `json:"nextCursor"`
		}
		if !isObject(raw) || json.Unmarshal(raw, &page) != nil || page.Tools == nil {
			return nil, fmt.Errorf("MCP server %s: invalid tools/list result", c.name)
		}
		for _, rawTool := range page.Tools {
			var d declaration
			if !isObject(rawTool) || json.Unmarshal(rawTool, &d) != nil || d.Name == "" || !isObject(d.InputSchema) {
				return nil, fmt.Errorf("MCP server %s: invalid tool declaration", c.name)
			}
			if seenTools[d.Name] {
				return nil, fmt.Errorf("MCP server %s: duplicate upstream tool %q", c.name, d.Name)
			}
			seenTools[d.Name] = true
			d.raw = bytes.Clone(rawTool)
			tools = append(tools, d)
		}
		if page.NextCursor == "" {
			break
		}
		if seenCursors[page.NextCursor] {
			return nil, fmt.Errorf("MCP server %s: repeated tools/list cursor", c.name)
		}
		seenCursors[page.NextCursor] = true
		cursor = page.NextCursor
	}
	allowed := map[string]bool{}
	for _, name := range c.config.Tools {
		if !seenTools[name] {
			return nil, fmt.Errorf("MCP server %s: allowlisted tool %q was not discovered", c.name, name)
		}
		allowed[name] = true
	}
	catalog := &Catalog{connection: c, byName: make(map[string]int)}
	for _, d := range tools {
		if c.config.Tools != nil && !allowed[d.Name] {
			continue
		}
		catalog.byName[d.Name] = len(catalog.tools)
		catalog.tools = append(catalog.tools, d)
	}
	return catalog, nil
}

type declaration struct {
	Name        string          `json:"name"`
	Description string          `json:"description"`
	InputSchema json.RawMessage `json:"inputSchema"`
	raw         json.RawMessage
}

func (c *Catalog) lookup(name string) (declaration, error) {
	index, ok := c.byName[name]
	if !ok {
		return declaration{}, fmt.Errorf("MCP server %s: unknown or excluded tool %q; run %s --list", c.connection.name, name, c.connection.name)
	}
	return c.tools[index], nil
}

// Call preserves JSON numbers and all content/metadata in the returned result.
// Only tools in this catalog may be invoked, including through CLI JSON input.
func (c *Catalog) Call(ctx context.Context, name string, arguments json.RawMessage) (json.RawMessage, error) {
	if _, err := c.lookup(name); err != nil {
		return nil, err
	}
	if !isObject(arguments) {
		return nil, fmt.Errorf("MCP tool arguments must be a JSON object")
	}
	ctx, cancel := context.WithTimeout(ctx, timeout(c.connection.config.TimeoutSeconds, 300*time.Second))
	defer cancel()
	raw, err := c.connection.request(ctx, "tools/call", struct {
		Name      string          `json:"name"`
		Arguments json.RawMessage `json:"arguments"`
	}{name, arguments})
	if err != nil {
		return nil, err
	}
	var result struct {
		Content []json.RawMessage `json:"content"`
		IsError bool              `json:"isError"`
	}
	if !isObject(raw) || json.Unmarshal(raw, &result) != nil || result.Content == nil {
		return raw, fmt.Errorf("MCP server %s: invalid tools/call result; raw response written to stdout", c.connection.name)
	}
	if result.IsError {
		return raw, fmt.Errorf("MCP server %s: tool %q reported isError; see result on stdout", c.connection.name, name)
	}
	return raw, nil
}

func isObject(raw []byte) bool {
	raw = bytes.TrimSpace(raw)
	return len(raw) > 0 && raw[0] == '{' && json.Valid(raw)
}
