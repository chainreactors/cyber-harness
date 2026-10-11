package main

import (
	"bytes"
	"context"
	"encoding/json"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"

	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func TestJSONConfiguredCLIListsAndCallsMCP(t *testing.T) {
	s := server.NewMCPServer("example-test", "1", server.WithToolCapabilities(false))
	s.AddTool(mcpsdk.NewTool("echo", mcpsdk.WithString("value")), func(_ context.Context, request mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		return mcpsdk.NewToolResultText(request.GetString("value", "")), nil
	})
	upstream := httptest.NewServer(server.NewStreamableHTTPServer(s))
	defer upstream.Close()
	path := filepath.Join(t.TempDir(), "mcp.json")
	data, _ := json.Marshal(map[string]any{"mcpServers": map[string]any{"demo": map[string]any{"url": upstream.URL + "/mcp"}}})
	if err := os.WriteFile(path, data, 0600); err != nil {
		t.Fatal(err)
	}
	var output bytes.Buffer
	if err := run(context.Background(), path, nil, &output, &output); err != nil || !strings.Contains(output.String(), "demo --list") {
		t.Fatalf("list commands: %v, %s", err, &output)
	}
	output.Reset()
	if err := run(context.Background(), path, []string{"demo", "echo", "--value", "from-example"}, &output, &output); err != nil || !strings.Contains(output.String(), "from-example") || !json.Valid(bytes.TrimSpace(output.Bytes())) {
		t.Fatalf("call: %v, %s", err, &output)
	}
}
