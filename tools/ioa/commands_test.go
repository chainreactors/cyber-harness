package ioa

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"strings"
	"testing"

	"github.com/chainreactors/cyber/agent"
	"github.com/chainreactors/cyber/core/operation"
	coretool "github.com/chainreactors/cyber/core/tool"
	"github.com/chainreactors/cyber/internal/testutil/hosttest"
	terminaltool "github.com/chainreactors/cyber/tools/terminal"
	"github.com/chainreactors/ioa/protocols"
)

type captureWriter struct {
	bytes.Buffer
}

func (w *captureWriter) Reset(_ io.Writer) { w.Buffer.Reset() }
func (w *captureWriter) Captured() string  { return w.String() }

var testOutput = &captureWriter{}

const knownSpaceID = "a34763e95c29179802a4451597446c35"

// ---------------------------------------------------------------------------
// ioa space subcommands
// ---------------------------------------------------------------------------

func TestSpaceJoinExplicit(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)

	testOutput.Reset(nil)
	if err := findSubCmd(t, cmds, "space").Execute(context.Background(), []string{"my-space", "test"}); err != nil {
		t.Fatalf("ioa space join: %v", err)
	}
	if len(client.spaceCalls) != 1 || client.spaceCalls[0] != "my-space" {
		t.Fatalf("space calls = %v, want [my-space]", client.spaceCalls)
	}
	out := testOutput.Captured()
	if !strings.Contains(out, knownSpaceID) {
		t.Fatalf("output should contain space ID, got: %s", out)
	}
}

func TestSpaceJoinWithTags(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)

	if err := findSubCmd(t, cmds, "space").Execute(context.Background(), []string{
		"my-space", "test", "--tag", "recon",
	}); err != nil {
		t.Fatalf("ioa space with tags: %v", err)
	}
	if len(client.spaceCalls) != 1 {
		t.Fatalf("space calls = %d, want 1", len(client.spaceCalls))
	}
}

func TestSpaceJoinMissingArgs(t *testing.T) {
	cmds := NewCommands(newFakeIOAClient(), "tester", nil)

	tests := []struct {
		name string
		args []string
	}{
		{"no args", []string{}},
		{"name only", []string{"x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			testOutput.Reset(nil)
			err := findSubCmd(t, cmds, "space").Execute(context.Background(), tt.args)
			if err == nil {
				t.Fatal("expected error")
			}
		})
	}
}

func TestSpaceList(t *testing.T) {
	client := newFullFakeIOAClient(
		protocols.SpaceInfo{ID: "s1", Name: "space-one"},
		protocols.SpaceInfo{ID: "s2", Name: "space-two"},
	)
	cmds := NewCommands(client, "tester", nil)

	testOutput.Reset(nil)
	if err := findSubCmd(t, cmds, "space").Execute(context.Background(), []string{"list"}); err != nil {
		t.Fatalf("ioa space list: %v", err)
	}
	out := testOutput.Captured()
	if !strings.Contains(out, "space-one") || !strings.Contains(out, "space-two") {
		t.Fatalf("list output should contain both spaces, got: %s", out)
	}
}

func TestSpaceNodes(t *testing.T) {
	client := newFullFakeIOAClient(protocols.SpaceInfo{
		ID: knownSpaceID, Name: "test-space",
		Nodes: []protocols.Node{{ID: "n1", Name: "scanner-01"}},
	})
	cmds := NewCommands(client, "tester", nil)
	joinSpaceByName(t, cmds, "test-space")

	testOutput.Reset(nil)
	if err := findSubCmd(t, cmds, "space").Execute(context.Background(), []string{"nodes"}); err != nil {
		t.Fatalf("ioa space nodes: %v", err)
	}
	out := testOutput.Captured()
	if !strings.Contains(out, "scanner-01") {
		t.Fatalf("nodes output should contain node name, got: %s", out)
	}
}

func TestSpaceNodesWithoutJoin(t *testing.T) {
	cmds := NewCommands(newFullFakeIOAClient(), "tester", nil)
	testOutput.Reset(nil)
	err := findSubCmd(t, cmds, "space").Execute(context.Background(), []string{"nodes"})
	if err == nil || !strings.Contains(err.Error(), "no space joined") {
		t.Fatalf("expected 'no space joined' error, got: %v", err)
	}
}

func TestSpaceTopics(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	client.messages = []protocols.Message{
		{ID: "root-1", Sender: "n1", Content: map[string]interface{}{"content": "topic A"}},
		{ID: "reply-1", Sender: "n2", Content: map[string]interface{}{"content": "re"}, Refs: protocols.Ref{Messages: []string{"root-1"}}},
		{ID: "root-2", Sender: "n1", Content: map[string]interface{}{"content": "topic B"}},
	}
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	testOutput.Reset(nil)
	if err := findSubCmd(t, cmds, "space").Execute(context.Background(), []string{"topics"}); err != nil {
		t.Fatalf("ioa space topics: %v", err)
	}
	out := testOutput.Captured()
	if strings.Contains(out, "reply-1") {
		t.Fatalf("topics should not include reply messages, got: %s", out)
	}
	if !strings.Contains(out, "root-1") || !strings.Contains(out, "root-2") {
		t.Fatalf("topics should include root messages, got: %s", out)
	}
}

func TestSpaceUnknownSubcommand(t *testing.T) {
	cmds := NewCommands(newFakeIOAClient(), "tester", nil)
	testOutput.Reset(nil)
	err := findCmd(t, cmds, "ioa").Execute(context.Background(), []string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("expected unknown subcommand error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ioa send subcommands
// ---------------------------------------------------------------------------

func TestSendBroadcast(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	if err := findSubCmd(t, cmds, "send").Execute(context.Background(), []string{
		"--content", `{"content":"hello"}`,
	}); err != nil {
		t.Fatalf("ioa send: %v", err)
	}
	if len(client.sentSpaceIDs) != 1 || client.sentSpaceIDs[0] != knownSpaceID {
		t.Fatalf("sent to %v, want [%s]", client.sentSpaceIDs, knownSpaceID)
	}
	if client.lastSentBody.Refs != nil {
		t.Fatalf("broadcast should have no refs, got %+v", client.lastSentBody.Refs)
	}
}

func TestSendToNode(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	if err := findSubCmd(t, cmds, "send").Execute(context.Background(), []string{
		"--ref-nodes", "target-node-42", "--content", `{"content":"hi"}`,
	}); err != nil {
		t.Fatalf("ioa send to: %v", err)
	}
	if client.lastSentBody.Refs == nil || len(client.lastSentBody.Refs.Nodes) != 1 || client.lastSentBody.Refs.Nodes[0] != "target-node-42" {
		t.Fatalf("send to node refs = %+v, want nodes=[target-node-42]", client.lastSentBody.Refs)
	}
}

func TestSendInvalidContent(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	testOutput.Reset(nil)
	err := findSubCmd(t, cmds, "send").Execute(context.Background(), []string{
		"--content", "not-json",
	})
	if err == nil || !strings.Contains(err.Error(), "invalid content JSON") {
		t.Fatalf("expected invalid content JSON error, got: %v", err)
	}
}

func TestSendReply(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	if err := findSubCmd(t, cmds, "send").Execute(context.Background(), []string{
		"--ref-messages", "msg-99", "--content", `{"content":"noted"}`,
	}); err != nil {
		t.Fatalf("ioa send reply: %v", err)
	}
	if client.lastSentBody.Refs == nil || len(client.lastSentBody.Refs.Messages) != 1 || client.lastSentBody.Refs.Messages[0] != "msg-99" {
		t.Fatalf("reply refs = %+v, want messages=[msg-99]", client.lastSentBody.Refs)
	}
}

func TestSendPositionalRecipientWithJSON(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	testOutput.Reset(nil)
	err := findSubCmd(t, cmds, "send").Execute(context.Background(), []string{
		"nosuchproto", "--content", `{"content":"x"}`,
	})
	if err != nil || client.lastSentBody.Meta["target_session_id"] != "nosuchproto" {
		t.Fatalf("expected positional recipient, got: %v", err)
	}
}

func TestSendWithoutSpace(t *testing.T) {
	cmds := NewCommands(newFakeIOAClient(), "tester", nil)
	testOutput.Reset(nil)
	err := findSubCmd(t, cmds, "send").Execute(context.Background(), []string{
		"--content", `{"content":"hello"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "ioa space") {
		t.Fatalf("expected space error, got: %v", err)
	}
}

func TestSendWithoutContent(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	testOutput.Reset(nil)
	err := findSubCmd(t, cmds, "send").Execute(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "--content") {
		t.Fatalf("expected content error, got: %v", err)
	}
}

func TestSendRejectsUnknownOption(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	testOutput.Reset(nil)
	err := findSubCmd(t, cmds, "send").Execute(context.Background(), []string{
		"white", "--bogus", "x", "--content", `{"content":"x"}`,
	})
	if err == nil {
		t.Fatal("unknown option accepted")
	}
}

func TestSendCheckpoint(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	testOutput.Reset(nil)
	if err := findSubCmd(t, cmds, "send").Execute(context.Background(), []string{
		"checkpoint",
		"--kind", "verify",
		"--title", "SQL Injection Found",
		"--content", "Confirmed via error-based injection on /login",
		"--target", "http://10.0.0.1:8080",
		"--status", "confirmed",
	}); err != nil {
		t.Fatalf("ioa send checkpoint: %v", err)
	}
	if len(client.sentSpaceIDs) != 1 || client.sentSpaceIDs[0] != knownSpaceID {
		t.Fatalf("sent to %v, want [%s]", client.sentSpaceIDs, knownSpaceID)
	}
	if client.lastSentBody.ContentType != "checkpoint" {
		t.Fatalf("content_type = %q, want checkpoint", client.lastSentBody.ContentType)
	}
	content := client.lastSentBody.Content
	if content["kind"] != "verify" {
		t.Fatalf("content kind = %v, want verify", content["kind"])
	}
	if content["title"] != "SQL Injection Found" {
		t.Fatalf("content title = %v", content["title"])
	}
	if content["target"] != "http://10.0.0.1:8080" {
		t.Fatalf("content target = %v", content["target"])
	}
	if content["status"] != "confirmed" {
		t.Fatalf("content status = %v", content["status"])
	}
}

func TestSendCheckpointKindOnly(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	testOutput.Reset(nil)
	// Upstream checkpoint protocol does not require title; it sends what it gets.
	err := findSubCmd(t, cmds, "send").Execute(context.Background(), []string{
		"checkpoint", "--kind", "verify",
	})
	if err != nil {
		t.Fatalf("ioa send checkpoint with kind only: %v", err)
	}
	if client.lastSentBody.ContentType != "checkpoint" {
		t.Fatalf("content_type = %q, want checkpoint", client.lastSentBody.ContentType)
	}
}

func TestSendCheckpointWithoutSpace(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)

	testOutput.Reset(nil)
	err := findSubCmd(t, cmds, "send").Execute(context.Background(), []string{
		"checkpoint", "--kind", "verify", "--title", "test",
	})
	if err == nil || !strings.Contains(err.Error(), "no space joined") {
		t.Fatalf("expected no-space error, got: %v", err)
	}
}

func TestSendHandoff(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	if err := findSubCmd(t, cmds, "send").Execute(context.Background(), []string{
		"handoff", "--title", "Delegate scan", "--message", "Inspect the target",
	}); err != nil {
		t.Fatalf("ioa send handoff: %v", err)
	}
	if client.lastSentBody.ContentType != "handoff" {
		t.Fatalf("content_type = %q, want handoff", client.lastSentBody.ContentType)
	}
	if client.lastSentBody.Content["title"] != "Delegate scan" || client.lastSentBody.Content["message"] != "Inspect the target" {
		t.Fatalf("content = %#v", client.lastSentBody.Content)
	}
}

// ---------------------------------------------------------------------------
// ioa read subcommands
// ---------------------------------------------------------------------------

func TestReadDefault(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	testOutput.Reset(nil)
	if err := findSubCmd(t, cmds, "read").Execute(context.Background(), nil); err != nil {
		t.Fatalf("ioa read: %v", err)
	}
	if client.lastReadOpts.All {
		t.Fatal("default read should not set All")
	}
}

func TestReadAll(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	if err := findSubCmd(t, cmds, "read").Execute(context.Background(), []string{
		"--all", "--limit", "10",
	}); err != nil {
		t.Fatalf("ioa read all: %v", err)
	}
	if !client.lastReadOpts.All {
		t.Fatal("ioa read all should set All=true")
	}
	if client.lastReadOpts.Limit != 10 {
		t.Fatalf("limit = %d, want 10", client.lastReadOpts.Limit)
	}
}

func TestReadThread(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	if err := findSubCmd(t, cmds, "read").Execute(context.Background(), []string{
		"--message", "msg-42",
	}); err != nil {
		t.Fatalf("ioa read thread: %v", err)
	}
	if client.lastReadOpts.MessageID != "msg-42" {
		t.Fatalf("message_id = %q, want msg-42", client.lastReadOpts.MessageID)
	}
}

func TestReadUnknownFlag(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	testOutput.Reset(nil)
	err := findSubCmd(t, cmds, "read").Execute(context.Background(), []string{"--bogus"})
	if err == nil {
		t.Fatalf("expected unknown flag error")
	}
}

func TestReadNew(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	if err := findSubCmd(t, cmds, "read").Execute(context.Background(), []string{
		"--after", "cursor-abc",
	}); err != nil {
		t.Fatalf("ioa read new: %v", err)
	}
	if client.lastReadOpts.After != "cursor-abc" {
		t.Fatalf("after = %q, want cursor-abc", client.lastReadOpts.After)
	}
}

func TestReadWithoutSpace(t *testing.T) {
	cmds := NewCommands(newFakeIOAClient(), "tester", nil)
	testOutput.Reset(nil)
	err := findSubCmd(t, cmds, "read").Execute(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "ioa space") {
		t.Fatalf("expected space error, got: %v", err)
	}
}

func TestReadUnknownSubcommand(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	testOutput.Reset(nil)
	err := findSubCmd(t, cmds, "read").Execute(context.Background(), []string{"bogus"})
	if err == nil {
		t.Fatalf("expected error for unknown subcommand")
	}
}

// ---------------------------------------------------------------------------
// default space binding
// ---------------------------------------------------------------------------

func TestDefaultSpaceSkipsJoin(t *testing.T) {
	client := newFakeIOAClient()
	root := &rootCommand{client: client, nodeName: "tester", binding: &spaceBinding{}}
	root.binding.set(knownSpaceID)
	cmds := root.commands()

	if err := findSubCmd(t, cmds, "send").Execute(context.Background(), []string{
		"--content", `{"content":"hello"}`,
	}); err != nil {
		t.Fatalf("ioa send with default space: %v", err)
	}
	if len(client.sentSpaceIDs) != 1 || client.sentSpaceIDs[0] != knownSpaceID {
		t.Fatalf("sent to %v, want [%s]", client.sentSpaceIDs, knownSpaceID)
	}
}

// ---------------------------------------------------------------------------
// LLM integration test
// ---------------------------------------------------------------------------

// TestLLMIOAToolUsage uses a real LLM to verify that the IOA tools are
// discoverable and usable through the agent's bash pseudo-command interface.
//
// Run with:
//
//	LIVE_TEST_API_KEY=sk-xxx \
//	go test -v -run TestLLMIOAToolUsage ./tools/ioa/ -timeout 120s
func TestLLMIOAToolUsage(t *testing.T) {
	apiKey := os.Getenv("LIVE_TEST_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("OPENAI_API_KEY")
	}
	if apiKey == "" {
		t.Skip("set LIVE_TEST_API_KEY or OPENAI_API_KEY to run live LLM IOA test")
	}
	baseURL := envOr("LIVE_TEST_BASE_URL", "https://api.deepseek.com")
	model := envOr("LIVE_TEST_MODEL", "deepseek-v4-pro")

	llm, err := agent.NewProvider(&agent.ProviderConfig{
		BaseURL: baseURL,
		APIKey:  apiKey,
		Model:   model,
		Timeout: 60,
	})
	if err != nil {
		t.Fatalf("NewProvider: %v", err)
	}

	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "test-space"})
	cmds := NewCommands(client, "llm-tester", nil)

	registry := hosttest.Commands(t, cmds...)
	dir := t.TempDir()
	bash := terminaltool.NewBashTool(dir, 30, nil)
	bash.SetCommandRegistry(registry)
	tools := hosttest.Tools(t, bash)
	t.Cleanup(bash.Close)

	systemPrompt := `You are a testing agent. You have IOA tools available as pseudo-commands through the bash tool.

Available pseudo-commands:
` + registry.UsageDocs() + `

IMPORTANT: These pseudo-commands run through the bash tool. Example: bash {"command": "ioa space test-space "integration test""}

Your task:
1. First, join the space named "test-space" with description "integration test"
2. Then send a broadcast message with content {"content": "test message from LLM"}
3. Then read all messages from the space
4. Finally, report what you did in plain text.

Execute each step one at a time.`

	t.Logf("System prompt:\n%s", systemPrompt)

	ag := agent.NewAgent(agent.Config{Loop: agent.StandardLoop{},
		Provider: llm,
		Tools:    tools,
		Model:    model,
	}.
		WithSystemPrompt(systemPrompt).
		WithStream(false))

	result, err := ag.Run(context.Background(), agent.TextInput("Execute the IOA integration test steps described in your instructions."))
	if err != nil {
		t.Fatalf("agent.Run: %v", err)
	}

	t.Logf("Agent output:\n%s", result.Output)
	t.Logf("Turns: %d, Total tokens: %d", result.Turns, result.TotalUsage.TotalTokens)

	// Verify the LLM actually called the tools
	if len(client.spaceCalls) == 0 {
		t.Error("LLM never called ioa space join")
	}
	if len(client.sentSpaceIDs) == 0 {
		t.Error("LLM never called ioa send")
	}
	if len(client.readSpaceIDs) == 0 {
		t.Error("LLM never called ioa read")
	}

	// Verify the correct space was used
	for _, id := range client.sentSpaceIDs {
		if id != knownSpaceID {
			t.Errorf("send used space %q, want %q", id, knownSpaceID)
		}
	}

	t.Logf("✓ space joins: %v", client.spaceCalls)
	t.Logf("✓ sends to spaces: %v", client.sentSpaceIDs)
	t.Logf("✓ reads from spaces: %v", client.readSpaceIDs)
	if client.lastSentBody.Content != nil {
		data, _ := json.Marshal(client.lastSentBody.Content)
		t.Logf("✓ last sent content: %s", data)
	}
}

func envOr(key, fallback string) string {
	if v := os.Getenv(key); v != "" {
		return v
	}
	return fallback
}

// ---------------------------------------------------------------------------
// helpers
// ---------------------------------------------------------------------------

func joinSpace(t *testing.T, cmds []coretool.Command) {
	t.Helper()
	joinSpaceByName(t, cmds, "my-space")
}

func joinSpaceByName(t *testing.T, cmds []coretool.Command, name string) {
	t.Helper()
	testOutput.Reset(nil)
	if err := findSubCmd(t, cmds, "space").Execute(context.Background(), []string{name, "test"}); err != nil {
		t.Fatalf("ioa space join %s: %v", name, err)
	}
}

type testCommand struct{ coretool.Command }

func (c testCommand) Execute(ctx context.Context, args []string) error {
	_, err := c.Run(ctx, &coretool.Execution{Args: args, Stdout: testOutput, Stderr: testOutput})
	return err
}

func findCmd(t *testing.T, cmds []coretool.Command, name string) testCommand {
	t.Helper()
	for _, cmd := range cmds {
		if cmd.Name == name {
			return testCommand{Command: cmd}
		}
	}
	t.Fatalf("command %q not found", name)
	return testCommand{}
}

// findSubCmd returns the ioa root command wrapped to dispatch the given
// subcommand (space/send/read), so tests read like the old ioa_* commands.
func findSubCmd(t *testing.T, cmds []coretool.Command, sub string) testCommand {
	t.Helper()
	root := findCmd(t, cmds, "ioa")
	inner := root.Run
	root.Run = func(ctx context.Context, execution *coretool.Execution) (any, error) {
		execution.Args = append([]string{sub}, execution.Args...)
		return inner(ctx, execution)
	}
	return root
}

// ---------------------------------------------------------------------------
// fake IOA client (basic — implements ioaclient.API)
// ---------------------------------------------------------------------------

type fakeIOAClient struct {
	nodeID       string
	spaces       map[string]protocols.SpaceInfo
	messages     []protocols.Message // returned by Read
	spaceCalls   []string
	sentSpaceIDs []string
	readSpaceIDs []string
	lastSentBody protocols.SendMessage
	lastReadOpts protocols.ReadOptions
}

func newFakeIOAClient(spaces ...protocols.SpaceInfo) *fakeIOAClient {
	c := &fakeIOAClient{spaces: make(map[string]protocols.SpaceInfo)}
	for _, s := range spaces {
		c.spaces[s.Name] = s
	}
	return c
}

func (c *fakeIOAClient) NodeID() string { return c.nodeID }

func (c *fakeIOAClient) RegisterNode(_ context.Context, name string, _ string, _ map[string]interface{}) (protocols.Node, error) {
	c.nodeID = "node-1"
	return protocols.Node{ID: c.nodeID, Name: name}, nil
}

func (c *fakeIOAClient) Space(_ context.Context, name, _ string, _ ...string) (protocols.SpaceInfo, error) {
	c.spaceCalls = append(c.spaceCalls, name)
	if s, ok := c.spaces[name]; ok {
		return s, nil
	}
	s := protocols.SpaceInfo{ID: "created-" + name, Name: name}
	c.spaces[name] = s
	return s, nil
}

func (c *fakeIOAClient) Send(_ context.Context, spaceID string, body protocols.SendMessage) (protocols.Message, error) {
	if body.Content == nil {
		return protocols.Message{}, fmt.Errorf("content is required")
	}
	c.sentSpaceIDs = append(c.sentSpaceIDs, spaceID)
	c.lastSentBody = body
	return protocols.Message{ID: "msg-sent", Sender: c.nodeID, Content: body.Content, Refs: derefRef(body.Refs)}, nil
}

func (c *fakeIOAClient) Read(_ context.Context, spaceID string, opts protocols.ReadOptions) ([]protocols.Message, error) {
	c.readSpaceIDs = append(c.readSpaceIDs, spaceID)
	c.lastReadOpts = opts
	if c.messages != nil {
		return c.messages, nil
	}
	return []protocols.Message{{ID: "msg-1", Sender: c.nodeID}}, nil
}

func derefRef(r *protocols.Ref) protocols.Ref {
	if r == nil {
		return protocols.Ref{}
	}
	return *r
}

// ---------------------------------------------------------------------------
// full fake IOA client (adds ListSpaces, GetSpaceInfo for space subcommands)
// ---------------------------------------------------------------------------

type fullFakeIOAClient struct {
	fakeIOAClient
	allSpaces []protocols.SpaceInfo
}

func newFullFakeIOAClient(spaces ...protocols.SpaceInfo) *fullFakeIOAClient {
	c := &fullFakeIOAClient{
		fakeIOAClient: *newFakeIOAClient(spaces...),
		allSpaces:     spaces,
	}
	return c
}

func (c *fullFakeIOAClient) ListSpaces(_ context.Context) ([]protocols.SpaceInfo, error) {
	return c.allSpaces, nil
}

func (c *fullFakeIOAClient) GetSpaceInfo(_ context.Context, spaceID string) (protocols.SpaceInfo, error) {
	for _, s := range c.allSpaces {
		if s.ID == spaceID {
			return s, nil
		}
	}
	return protocols.SpaceInfo{}, fmt.Errorf("space %q not found", spaceID)
}

func TestSendAddsSessionProvenance(t *testing.T) {
	nodeID := protocols.NewID()
	resource := New(Config{NodeID: nodeID}, nil)
	if err := resource.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer resource.Close(context.Background())
	commands := resource.Service.Commands()
	ctx := operation.ContextWithInvocation(t.Context(), operation.Invocation{SessionID: "source"})
	if err := findSubCmd(t, commands, "send").Execute(ctx, []string{"--target-session", "child", "--content", `{"text":"hello"}`, "--meta", `{"source_session_id":"forged","target_session_id":"wrong"}`}); err != nil {
		t.Fatal(err)
	}
	messages, err := resource.Service.Client().Read(ctx, resource.Service.ReceiveSpace(), protocols.ReadOptions{All: true})
	if err != nil || len(messages) != 1 {
		t.Fatalf("messages: %v %v", messages, err)
	}
	m := messages[0]
	if m.Meta["source_session_id"] != "source" || m.Meta["target_session_id"] != "child" || len(m.Refs.Nodes) != 1 || m.Refs.Nodes[0] != nodeID {
		t.Fatalf("provenance: %#v", m)
	}
	if m.Meta["interrupt"] != nil {
		t.Fatal("ordinary send requests interruption")
	}
	if err := findSubCmd(t, commands, "send").Execute(ctx, []string{"handoff", "--target-session=child", "--interrupt", "--title", "task", "--message", "work"}); err != nil {
		t.Fatal(err)
	}
	messages, err = resource.Service.Client().Read(ctx, resource.Service.ReceiveSpace(), protocols.ReadOptions{All: true})
	if err != nil || len(messages) != 2 || messages[1].Meta["source_session_id"] != "source" || messages[1].Meta["interrupt"] != true {
		t.Fatalf("typed send: %v %v", messages, err)
	}
}

func TestSendShortForm(t *testing.T) {
	for _, tt := range []struct {
		name      string
		args      []string
		text      string
		interrupt bool
		invalid   bool
	}{
		{name: "plain", args: []string{"white", "第 2 手 F4"}, text: "第 2 手 F4"},
		{name: "quoted JSON text", args: []string{"white", `a "quote" and {JSON}`}, text: `a "quote" and {JSON}`},
		{name: "interrupt", args: []string{"white", "change course", "--interrupt"}, text: "change course", interrupt: true},
		{name: "JSON", args: []string{"white", "--content", `{"text":"hello"}`}, text: "hello"},
		{name: "protocol name as recipient", args: []string{"handoff", "hello"}, text: "hello"},
		{name: "flag text in JSON", args: []string{"white", "--content", `{"text":"--interrupt"}`}, text: "--interrupt"},
		{name: "missing text", args: []string{"white"}, invalid: true},
		{name: "empty recipient", args: []string{"", "hello"}, invalid: true},
		{name: "two targets", args: []string{"white", "hello", "--target-session", "black"}, invalid: true},
		{name: "extra text", args: []string{"white", "hello", "extra"}, invalid: true},
		{name: "empty legacy target", args: []string{"--target-session=", "--content", `{"text":"hello"}`}, invalid: true},
	} {
		t.Run(tt.name, func(t *testing.T) {
			resource := New(Config{}, nil)
			if err := resource.Start(t.Context()); err != nil {
				t.Fatal(err)
			}
			defer resource.Close(context.Background())
			ctx := operation.ContextWithInvocation(t.Context(), operation.Invocation{SessionID: "black"})
			err := findSubCmd(t, resource.Service.Commands(), "send").Execute(ctx, tt.args)
			if tt.invalid {
				if err == nil {
					t.Fatal("invalid send accepted")
				}
				return
			}
			if err != nil {
				t.Fatal(err)
			}
			messages, err := resource.Service.Client().Read(ctx, resource.Service.ReceiveSpace(), protocols.ReadOptions{All: true})
			if err != nil || len(messages) != 1 {
				t.Fatalf("messages: %v %v", messages, err)
			}
			m := messages[0]
			urgent, _ := m.Meta["interrupt"].(bool)
			if m.Content["text"] != tt.text || m.Meta["target_session_id"] != tt.args[0] || m.Meta["source_session_id"] != "black" || urgent != tt.interrupt {
				t.Fatalf("wrong message: %#v", m)
			}
		})
	}
}

func TestSendProtocolBooleanAndHelp(t *testing.T) {
	resource := New(Config{}, nil)
	if err := resource.Start(t.Context()); err != nil {
		t.Fatal(err)
	}
	defer resource.Close(context.Background())
	command := findSubCmd(t, resource.Service.Commands(), "send")
	if err := command.Execute(t.Context(), []string{"swarm", "--task", "--interrupt", "--target-session", "worker", "--content=--interrupt"}); err != nil {
		t.Fatal(err)
	}
	rows, err := resource.Service.Client().Read(t.Context(), resource.Service.ReceiveSpace(), protocols.ReadOptions{All: true})
	if err != nil || len(rows) != 1 || rows[0].Meta["interrupt"] != true || rows[0].Content["content"] != "--interrupt" {
		t.Fatalf("protocol flags: %v %v", rows, err)
	}
	if err := command.Execute(t.Context(), []string{"handoff", "--help"}); err != nil {
		t.Fatal(err)
	}
}
