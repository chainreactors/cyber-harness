package mcp

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/core/egress"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
	coretool "github.com/chainreactors/cyber/core/tool"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
	terminalext "github.com/chainreactors/cyber/exts/terminal"
	mcptools "github.com/chainreactors/cyber/tools/mcp"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
	mcpsdk "github.com/mark3labs/mcp-go/mcp"
	"github.com/mark3labs/mcp-go/server"
)

func testServer(calls *atomic.Int32) *server.MCPServer {
	s := server.NewMCPServer("extension-test", "1", server.WithToolCapabilities(false))
	s.AddTool(mcpsdk.NewTool("echo", mcpsdk.WithString("message")), func(_ context.Context, request mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		calls.Add(1)
		cwd, _ := os.Getwd()
		return mcpsdk.NewToolResultText(fmt.Sprintf("%s|%s|%s", request.GetString("message", ""), cwd, os.Getenv("CYBER_MCP_VALUE"))), nil
	})
	s.AddTool(mcpsdk.NewTool("tool.with.dots/"+strings.Repeat("a", 80), mcpsdk.WithObject("nested")), func(_ context.Context, request mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		calls.Add(1)
		data, _ := json.Marshal(request.GetArguments())
		return mcpsdk.NewToolResultText(string(data)), nil
	})
	s.AddTool(mcpsdk.NewTool("fail"), func(context.Context, mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		calls.Add(1)
		return mcpsdk.NewToolResultError("partial failure"), nil
	})
	s.AddTool(mcpsdk.NewTool("wait"), func(ctx context.Context, _ mcpsdk.CallToolRequest) (*mcpsdk.CallToolResult, error) {
		if marker := os.Getenv("CYBER_MCP_MARKER"); marker != "" {
			_ = os.WriteFile(marker, []byte("started"), 0600)
		}
		select {
		case <-ctx.Done():
			return nil, ctx.Err()
		case <-time.After(time.Minute):
			return mcpsdk.NewToolResultText("finished"), nil
		}
	})
	return s
}

// A real MCP child process with no external interpreter/engine dependency.
func TestMCPHelper(t *testing.T) {
	if os.Getenv("CYBER_MCP_HELPER") != "1" {
		return
	}
	if os.Getenv("CYBER_MCP_STALL") == "1" {
		time.Sleep(time.Minute)
		os.Exit(0)
	}
	_, _ = fmt.Fprint(os.Stderr, strings.Repeat("diagnostic\n", 20000))
	err := server.NewStdioServer(testServer(new(atomic.Int32))).Listen(context.Background(), os.Stdin, os.Stdout)
	if err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}

type testHost struct {
	commands *coretool.CommandRegistry
	tools    *coretool.ToolRegistry
	hooks    *hooks.Registry
	set      *extension.Set
	bash     *terminaltool.BashTool
	dir      string
}

func assembly(t *testing.T, config Config) *testHost {
	t.Helper()
	h := &testHost{commands: coretool.NewCommandRegistry(), tools: coretool.NewToolRegistry(), hooks: hooks.New(), dir: t.TempDir()}
	set, err := extension.New(
		extension.Provided[*hooks.Registry](h.hooks), extension.Provided[egress.Endpoint](egress.Disabled()), h.commands, h.tools,
		terminalext.New(terminalext.Config{Directory: h.dir, Timeout: 10}), New(config),
		extension.Func{LoadFunc: func(scope *extension.Scope) error {
			var err error
			h.bash, err = extension.Use[*terminaltool.BashTool](scope)
			return err
		}},
	)
	if err != nil {
		t.Fatal(err)
	}
	h.set = set
	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := set.Close(ctx); err != nil {
			t.Error(err)
		}
	})
	return h
}

func helperConfig(t *testing.T, directory string) mcptools.ServerConfig {
	t.Helper()
	executable, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return mcptools.ServerConfig{Command: executable, Args: []string{"-test.run=^TestMCPHelper$"}, Cwd: directory,
		Env: map[string]string{"CYBER_MCP_HELPER": "1", "CYBER_MCP_VALUE": "chosen-value"}}
}

func (h *testHost) command(ctx context.Context, name string, args ...string) (string, error) {
	var output bytes.Buffer
	_, err := h.commands.Execute(ctx, name, &coretool.Execution{Args: args, Dir: h.dir, Stdout: &output})
	return output.String(), err
}

func TestStdioBashWorkflowAndLifetime(t *testing.T) {
	directory := t.TempDir()
	h := assembly(t, Config{MCPServers: map[string]mcptools.ServerConfig{"ida": helperConfig(t, directory)}})
	init, cancel := context.WithCancel(context.Background())
	if err := h.set.Load(init); err != nil {
		t.Fatal(err)
	}
	cancel() // The child must survive cancellation of the short load context.
	definitions := h.tools.ToolDefinitions()
	if len(definitions) != 1 || definitions[0].Name != "bash" || !strings.Contains(h.commands.UsageDocs(), "ida --list") {
		t.Fatal("MCP should be discoverable as CLI through bash")
	}
	arguments := `{"command":"ida echo --message hello"}`
	result, err := h.tools.ExecuteTool(context.Background(), "bash", arguments)
	if err != nil || result.IsError || !strings.Contains(coretool.ResultText(result), "hello|") || !strings.Contains(coretool.ResultText(result), "chosen-value") {
		t.Fatalf("stdio call through AI bash boundary: %v %v", result, err)
	}
	output, err := h.command(context.Background(), "ida", "--list")
	if err != nil || !strings.Contains(output, "echo") {
		t.Fatalf("catalog: %s %v", output, err)
	}
	if err := h.set.Close(context.Background()); err != nil {
		t.Fatal(err)
	}
	if h.commands.Has("ida") {
		t.Fatal("command remained visible after unload")
	}
}

func TestStdioUnloadCancelsInFlightCall(t *testing.T) {
	directory := t.TempDir()
	marker := filepath.Join(directory, "started")
	config := helperConfig(t, directory)
	config.Env["CYBER_MCP_MARKER"] = marker
	h := assembly(t, Config{MCPServers: map[string]mcptools.ServerConfig{"ida": config}})
	if err := h.set.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	finished := make(chan error, 1)
	go func() { _, err := h.command(context.Background(), "ida", "wait"); finished <- err }()
	deadline := time.Now().Add(5 * time.Second)
	for {
		if _, err := os.Stat(marker); err == nil {
			break
		}
		if time.Now().After(deadline) {
			t.Fatal("helper tool did not start")
		}
		time.Sleep(10 * time.Millisecond)
	}
	closeCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if err := h.set.Close(closeCtx); err != nil {
		t.Fatal(err)
	}
	select {
	case err := <-finished:
		if !errors.Is(err, context.Canceled) {
			t.Fatalf("in-flight command should be canceled: %v", err)
		}
	case <-time.After(time.Second):
		t.Fatal("registry failed to drain in-flight command")
	}
}

func TestHTTPBashCompositionAndAdmission(t *testing.T) {
	var calls atomic.Int32
	upstream := httptest.NewServer(server.NewStreamableHTTPServer(testServer(&calls)))
	defer upstream.Close()
	h := assembly(t, Config{MCPServers: map[string]mcptools.ServerConfig{"http": {URL: upstream.URL + "/mcp"}}})
	if err := h.set.Load(context.Background()); err != nil {
		t.Fatal(err)
	}
	// Native CLI participates in variable expansion, pipelines and redirects.
	script := `message='two words'; http echo --message "$message" | { read -r result; printf '%s\n' "$result"; } > result.json`
	var output bytes.Buffer
	execution, err := h.bash.RunForeground(context.Background(), script, terminaltool.BashExecOptions{Stdout: &output})
	if err != nil {
		t.Fatal(err)
	}
	info, _ := execution.Session()
	declaration, err := os.ReadFile(filepath.Join(h.dir, "result.json"))
	if err != nil || info.ExitStatus() != 0 || !strings.Contains(string(declaration), "two words|") || !json.Valid(bytes.TrimSpace(declaration)) || calls.Load() != 1 {
		t.Fatalf("composed CLI: %s %v status=%d calls=%d", declaration, err, info.ExitStatus(), calls.Load())
	}
	name := "tool.with.dots/" + strings.Repeat("a", 80)
	if err := os.WriteFile(filepath.Join(h.dir, "args.json"), []byte(`{"nested":{"array":[true,"hello",{"value":null}]}}`), 0600); err != nil {
		t.Fatal(err)
	}
	text, err := h.command(context.Background(), "http", name, "--file", "args.json")
	if err != nil || !strings.Contains(text, "hello") {
		t.Fatalf("arbitrary tool name and nested arguments: %s %v", text, err)
	}
	// Tool failures retain stdout and fail the shell command.
	result, err := h.bash.RunForegroundTool(context.Background(), "http fail > failure.json", terminaltool.BashExecOptions{})
	failure, readErr := os.ReadFile(filepath.Join(h.dir, "failure.json"))
	if err != nil || !result.IsError || readErr != nil || !strings.Contains(string(failure), `"isError":true`) {
		t.Fatalf("failure lost stdout or exit status: %v %v %s %v", result, err, failure, readErr)
	}
	result, err = h.bash.RunForegroundTool(context.Background(), "http fail > failure.json 2> diagnostics.txt || printf recovered", terminaltool.BashExecOptions{})
	if err != nil || result.IsError || !strings.Contains(coretool.ResultText(result), "recovered") {
		t.Fatalf("shell recovery failed: %v %v", result, err)
	}
	before := calls.Load()
	denial := toolhooks.BeforeCommand.On(h.hooks, "deny-mcp", func(_ context.Context, event toolhooks.CommandEvent) (toolhooks.Admission, error) {
		if event.Name == "http" && len(event.Args) > 0 && event.Args[0] == "echo" {
			return toolhooks.Admission{Deny: errors.New("denied by test")}, nil
		}
		return toolhooks.Admission{}, nil
	})
	defer denial.Cancel()
	if _, err := h.command(context.Background(), "http", "echo"); !errors.Is(err, operation.ErrDenied) || calls.Load() != before {
		t.Fatalf("admission bypassed: %v calls=%d", err, calls.Load())
	}
}

func TestStdioInitializationTimeoutRollsBackOwnedProcess(t *testing.T) {
	config := helperConfig(t, t.TempDir())
	config.Env["CYBER_MCP_STALL"] = "1"
	h := assembly(t, Config{MCPServers: map[string]mcptools.ServerConfig{"stall": config}})
	ctx, cancel := context.WithTimeout(context.Background(), 150*time.Millisecond)
	defer cancel()
	if err := h.set.Load(ctx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("initialization deadline lost: %v", err)
	}
	if h.commands.Has("stall") {
		t.Fatal("failed initialization exposed a command")
	}
	if err := h.set.Close(context.Background()); err != nil {
		t.Fatalf("owned process cleanup did not finish: %v", err)
	}
}

func TestHTTPRollbackAndAllowlist(t *testing.T) {
	var deletes atomic.Int32
	mcpHTTP := server.NewStreamableHTTPServer(testServer(new(atomic.Int32)))
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.Method == http.MethodDelete {
			deletes.Add(1)
		}
		mcpHTTP.ServeHTTP(w, r)
	}))
	defer upstream.Close()
	t.Run("rollback", func(t *testing.T) {
		before := deletes.Load()
		h := assembly(t, Config{MCPServers: map[string]mcptools.ServerConfig{
			"a": {URL: upstream.URL + "/mcp"}, "z": {URL: upstream.URL + "/mcp", Tools: []string{"absent"}},
		}})
		if err := h.set.Load(context.Background()); err == nil {
			t.Fatal("missing allowlisted tool did not fail load")
		}
		if len(h.commands.Names()) != 0 || deletes.Load()-before != 2 {
			t.Fatalf("partial load not rolled back: commands=%v deleted=%d", h.commands.Names(), deletes.Load()-before)
		}
	})
	for _, test := range []struct {
		name  string
		tools []string
		count int
	}{
		{"allowlist", []string{"echo"}, 1}, {"empty", []string{}, 0},
	} {
		t.Run(test.name, func(t *testing.T) {
			h := assembly(t, Config{MCPServers: map[string]mcptools.ServerConfig{"filtered": {URL: upstream.URL + "/mcp", Tools: test.tools}}})
			if err := h.set.Load(context.Background()); err != nil {
				t.Fatal(err)
			}
			output, err := h.command(context.Background(), "filtered", "--list")
			var summaries []any
			if err != nil || json.Unmarshal([]byte(output), &summaries) != nil || len(summaries) != test.count {
				t.Fatalf("allowlist semantics: %s %v", output, err)
			}
			if _, err := h.command(context.Background(), "filtered", "fail"); err == nil {
				t.Fatal("excluded tool was callable")
			}
		})
	}
}

func TestAliasRejectsBashNamespaceConflictsBeforeConnecting(t *testing.T) {
	var requests atomic.Int32
	upstream := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		requests.Add(1)
		w.WriteHeader(http.StatusInternalServerError)
	}))
	defer upstream.Close()
	for _, name := range []string{"echo", "test", "printf", "if", "for", "declare"} {
		t.Run(name, func(t *testing.T) {
			h := assembly(t, Config{MCPServers: map[string]mcptools.ServerConfig{
				"a": {URL: upstream.URL}, name: {URL: upstream.URL},
			}})
			if err := h.set.Load(context.Background()); err == nil || !strings.Contains(err.Error(), "conflicts with a bash") {
				t.Fatalf("unreachable alias accepted: %v", err)
			}
			if requests.Load() != 0 || len(h.commands.Names()) != 0 {
				t.Fatal("validation started a server or exposed commands")
			}
		})
	}
}
