package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"

	"github.com/chainreactors/aiscan/pkg/agent"
	"github.com/chainreactors/aiscan/pkg/provider"
	"github.com/chainreactors/aiscan/pkg/scanner"
	"github.com/chainreactors/aiscan/pkg/command"
	"github.com/chainreactors/aiscan/skills"
)

func TestScannerSubcommandsThroughBash(t *testing.T) {
	reg := scanner.NewScannerRegistry()
	commands := map[string]*recordingCommand{
		"gogo":    newRecordingCommand("gogo"),
		"spray":   newRecordingCommand("spray"),
		"zombie":  newRecordingCommand("zombie"),
		"neutron": newRecordingCommand("neutron"),
	}
	for _, name := range []string{"gogo", "spray", "zombie", "neutron"} {
		reg.Register(commands[name])
	}

	bash := command.NewBashTool(t.TempDir(), 5, reg)
	tests := []struct {
		name string
		cmd  string
		args []string
	}{
		{
			name: "gogo",
			cmd:  "gogo -i 127.0.0.1 -p 80,443 -t 10 -d 1 -vv",
			args: []string{"-i", "127.0.0.1", "-p", "80,443", "-t", "10", "-d", "1", "-vv"},
		},
		{
			name: "spray",
			cmd:  `spray -u "http://127.0.0.1/a b" -T 1 -t 5 --finger`,
			args: []string{"-u", "http://127.0.0.1/a b", "-T", "1", "-t", "5", "--finger"},
		},
		{
			name: "zombie",
			cmd:  "zombie -i ssh://root@127.0.0.1:22 -p pass -t 1 --top 3",
			args: []string{"-i", "ssh://root@127.0.0.1:22", "-p", "pass", "-t", "1", "--top", "3"},
		},
		{
			name: "neutron",
			cmd:  "neutron -i http://127.0.0.1 --finger nginx",
			args: []string{"-i", "http://127.0.0.1", "--finger", "nginx"},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			out, err := bash.Execute(context.Background(), bashArgs(tt.cmd))
			if err != nil {
				t.Fatalf("bash.Execute() error = %v", err)
			}
			if !strings.Contains(out, "["+tt.name+"] ok") {
				t.Fatalf("output = %q, want command output", out)
			}
			if got := commands[tt.name].lastArgs(); !reflect.DeepEqual(got, tt.args) {
				t.Fatalf("args = %#v, want %#v", got, tt.args)
			}
		})
	}
}

func TestAgentAutomaticWorkflowUsesScan(t *testing.T) {
	reg := scanner.NewScannerRegistry()
	scan := newRecordingCommand("scan")
	scan.output = "[scan] completed: inputs=1 open=1 web=1 weakpass=0 vulns=1 errors=0\n[vuln] http://127.0.0.1 template=test-cve severity=high"
	reg.Register(scan)
	reg.Register(newRecordingCommand("gogo"))
	reg.Register(newRecordingCommand("spray"))
	reg.Register(newRecordingCommand("zombie"))
	reg.Register(newRecordingCommand("neutron"))

	tools := command.NewRegistry()
	tools.Register(command.NewBashTool(t.TempDir(), 5, reg))

	llm := &scriptedProvider{
		responses: []*provider.ChatCompletionResponse{
			chatResponse(provider.ChatMessage{
				Role: "assistant",
				ToolCalls: []provider.ToolCall{
					{
						ID:   "call-1",
						Type: "function",
						Function: provider.FunctionCall{
							Name:      "bash",
							Arguments: bashArgs("scan -i 127.0.0.1 --mode quick"),
						},
					},
				},
			}),
			chatResponse(provider.NewTextMessage("assistant", "final report: one high severity vulnerability")),
		},
	}

	systemPrompt := agent.BuildSystemPrompt(&agent.PromptConfig{
		Tools:       tools,
		ScannerDocs: reg.UsageDocs(),
	})
	a := agent.New(llm, tools,
		agent.WithSystemPrompt(systemPrompt),
		agent.WithModel("test-model"),
	)

	result, err := a.Run(context.Background(), "scan 127.0.0.1 and summarize findings")
	if err != nil {
		t.Fatalf("agent.Run() error = %v", err)
	}
	if result != "final report: one high severity vulnerability" {
		t.Fatalf("result = %q", result)
	}
	if got := scan.lastArgs(); !reflect.DeepEqual(got, []string{"-i", "127.0.0.1", "--mode", "quick"}) {
		t.Fatalf("scan args = %#v", got)
	}

	requests := llm.requestsSnapshot()
	if len(requests) != 2 {
		t.Fatalf("provider calls = %d, want 2", len(requests))
	}
	if len(requests[0].Tools) != 1 || requests[0].Tools[0].Function.Name != "bash" {
		t.Fatalf("first provider request tools = %#v", requests[0].Tools)
	}
	if !hasToolMessage(requests[1].Messages, "call-1", "[scan] completed") {
		t.Fatalf("second provider request did not include scan tool result: %#v", requests[1].Messages)
	}
}

func TestAgentPromptIncludesEmbeddedSkillIndexAndExpansion(t *testing.T) {
	reg := scanner.NewScannerRegistry()
	tools := command.NewRegistry()
	store, diagnostics := skills.LoadEmbeddedStore()
	if len(diagnostics) != 0 {
		t.Fatalf("diagnostics = %#v", diagnostics)
	}
	tools.Register(command.NewReadTool(t.TempDir(), store))

	llm := &scriptedProvider{
		responses: []*provider.ChatCompletionResponse{
			chatResponse(provider.NewTextMessage("assistant", "done")),
		},
	}
	systemPrompt := agent.BuildSystemPrompt(&agent.PromptConfig{
		Tools:       tools,
		ScannerDocs: reg.UsageDocs(),
		Skills:      store.Skills,
	})
	task := skills.ExpandCommand("/skill:scan scan 127.0.0.1", store)

	result, err := agent.Run(context.Background(), task, tools,
		agent.WithProvider(llm),
		agent.WithSystemPrompt(systemPrompt),
		agent.WithModel("test-model"),
	)
	if err != nil {
		t.Fatalf("agent.Run() error = %v", err)
	}
	if result != "done" {
		t.Fatalf("result = %q", result)
	}
	requests := llm.requestsSnapshot()
	if len(requests) != 1 {
		t.Fatalf("provider calls = %d, want 1", len(requests))
	}
	if len(requests[0].Messages) < 2 {
		t.Fatalf("messages = %#v", requests[0].Messages)
	}
	system := requests[0].Messages[0]
	if system.Role != "system" || system.Content == nil || !strings.Contains(*system.Content, "<available_skills>") {
		t.Fatalf("system prompt missing skills: %#v", system)
	}
	user := requests[0].Messages[1]
	if user.Role != "user" || user.Content == nil || !strings.Contains(*user.Content, `<skill name="scan"`) || !strings.Contains(*user.Content, "scan 127.0.0.1") {
		t.Fatalf("user prompt missing expanded skill: %#v", user)
	}
}

func TestDirectScannerHelpIsNotReportedAsFailure(t *testing.T) {
	exe := buildAiscan(t)

	for _, name := range []string{"gogo", "spray", "zombie", "scan"} {
		t.Run(name, func(t *testing.T) {
			cmd := exec.Command(exe, name, "-h")
			out, err := cmd.CombinedOutput()
			if err != nil {
				t.Fatalf("%s -h failed: %v\n%s", name, err, out)
			}
			text := string(out)
			if strings.Contains(text, "scanner command failed") {
				t.Fatalf("%s -h reported scanner failure:\n%s", name, text)
			}
			if count := strings.Count(text, "Usage:"); count != 1 {
				t.Fatalf("%s -h Usage count = %d, want 1\n%s", name, count, text)
			}
		})
	}
}

type recordingCommand struct {
	name   string
	output string

	mu   sync.Mutex
	args [][]string
}

func newRecordingCommand(name string) *recordingCommand {
	return &recordingCommand{name: name}
}

func (c *recordingCommand) Name() string { return c.name }

func (c *recordingCommand) Usage() string {
	return fmt.Sprintf("%s - test command\nUsage: %s [options]", c.name, c.name)
}

func (c *recordingCommand) Execute(_ context.Context, args []string) (string, error) {
	c.mu.Lock()
	defer c.mu.Unlock()

	copied := append([]string(nil), args...)
	c.args = append(c.args, copied)
	if c.output != "" {
		return c.output, nil
	}
	return fmt.Sprintf("[%s] ok args=%s", c.name, strings.Join(args, " ")), nil
}

func (c *recordingCommand) lastArgs() []string {
	c.mu.Lock()
	defer c.mu.Unlock()

	if len(c.args) == 0 {
		return nil
	}
	return append([]string(nil), c.args[len(c.args)-1]...)
}

type scriptedProvider struct {
	mu        sync.Mutex
	responses []*provider.ChatCompletionResponse
	requests  []*provider.ChatCompletionRequest
}

func (p *scriptedProvider) Name() string { return "scripted" }

func (p *scriptedProvider) ChatCompletion(_ context.Context, req *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.requests = append(p.requests, cloneRequest(req))
	if len(p.responses) == 0 {
		return nil, fmt.Errorf("no scripted response left")
	}
	resp := p.responses[0]
	p.responses = p.responses[1:]
	return resp, nil
}

func (p *scriptedProvider) requestsSnapshot() []*provider.ChatCompletionRequest {
	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]*provider.ChatCompletionRequest, 0, len(p.requests))
	for _, req := range p.requests {
		out = append(out, cloneRequest(req))
	}
	return out
}

func chatResponse(msg provider.ChatMessage) *provider.ChatCompletionResponse {
	return &provider.ChatCompletionResponse{
		Choices: []provider.Choice{{Message: msg}},
	}
}

func bashArgs(command string) string {
	data, err := json.Marshal(map[string]string{"command": command})
	if err != nil {
		panic(err)
	}
	return string(data)
}

func cloneRequest(req *provider.ChatCompletionRequest) *provider.ChatCompletionRequest {
	cloned := *req
	cloned.Messages = append([]provider.ChatMessage(nil), req.Messages...)
	cloned.Tools = append([]provider.ToolDefinition(nil), req.Tools...)
	return &cloned
}

func hasToolMessage(messages []provider.ChatMessage, toolCallID, contains string) bool {
	for _, msg := range messages {
		if msg.Role != "tool" || msg.ToolCallID != toolCallID || msg.Content == nil {
			continue
		}
		if strings.Contains(*msg.Content, contains) {
			return true
		}
	}
	return false
}

func buildAiscan(t *testing.T) string {
	t.Helper()

	exe := filepath.Join(t.TempDir(), "aiscan-test.exe")
	cmd := exec.Command("go", "build", "-tags", "emptytemplates noembed", "-o", exe, "./cmd/aiscan")
	cmd.Dir = repoRoot(t)
	out, err := cmd.CombinedOutput()
	if err != nil {
		t.Fatalf("build aiscan: %v\n%s", err, out)
	}
	return exe
}

func repoRoot(t *testing.T) string {
	t.Helper()

	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	return filepath.Clean(filepath.Join(wd, "..", ".."))
}
