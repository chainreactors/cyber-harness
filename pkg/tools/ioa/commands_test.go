package ioa

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/agent"
	tmuxpkg "github.com/chainreactors/aiscan/pkg/agent/tmux"
	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/ioa/protocols"
)

const knownSpaceID = "a34763e95c29179802a4451597446c35"

// ---------------------------------------------------------------------------
// ioa_space subcommands
// ---------------------------------------------------------------------------

func TestSpaceJoinExplicit(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)

	commands.Output.Reset(nil)
	if err := findCmd(t, cmds, "ioa_space").Execute(context.Background(), []string{
		"join", "--name", "my-space", "--description", "test",
	}); err != nil {
		t.Fatalf("ioa_space join: %v", err)
	}
	if len(client.spaceCalls) != 1 || client.spaceCalls[0] != "my-space" {
		t.Fatalf("space calls = %v, want [my-space]", client.spaceCalls)
	}
	out := commands.Output.Captured()
	if !strings.Contains(out, knownSpaceID) {
		t.Fatalf("output should contain space ID, got: %s", out)
	}
}

func TestSpaceJoinImplicit(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)

	// no "join" subcommand, should still work
	if err := findCmd(t, cmds, "ioa_space").Execute(context.Background(), []string{
		"--name", "my-space", "--description", "test",
	}); err != nil {
		t.Fatalf("ioa_space (implicit join): %v", err)
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
		{"no args", []string{"join"}},
		{"name only", []string{"join", "--name", "x"}},
		{"desc only", []string{"join", "--description", "x"}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			commands.Output.Reset(nil)
			err := findCmd(t, cmds, "ioa_space").Execute(context.Background(), tt.args)
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

	commands.Output.Reset(nil)
	if err := findCmd(t, cmds, "ioa_space").Execute(context.Background(), []string{"list"}); err != nil {
		t.Fatalf("ioa_space list: %v", err)
	}
	out := commands.Output.Captured()
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

	commands.Output.Reset(nil)
	if err := findCmd(t, cmds, "ioa_space").Execute(context.Background(), []string{"nodes"}); err != nil {
		t.Fatalf("ioa_space nodes: %v", err)
	}
	out := commands.Output.Captured()
	if !strings.Contains(out, "scanner-01") {
		t.Fatalf("nodes output should contain node name, got: %s", out)
	}
}

func TestSpaceNodesWithoutJoin(t *testing.T) {
	cmds := NewCommands(newFullFakeIOAClient(), "tester", nil)
	commands.Output.Reset(nil)
	err := findCmd(t, cmds, "ioa_space").Execute(context.Background(), []string{"nodes"})
	if err == nil || !strings.Contains(err.Error(), "ioa_space join") {
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

	commands.Output.Reset(nil)
	if err := findCmd(t, cmds, "ioa_space").Execute(context.Background(), []string{"topics"}); err != nil {
		t.Fatalf("ioa_space topics: %v", err)
	}
	out := commands.Output.Captured()
	if strings.Contains(out, "reply-1") {
		t.Fatalf("topics should not include reply messages, got: %s", out)
	}
	if !strings.Contains(out, "root-1") || !strings.Contains(out, "root-2") {
		t.Fatalf("topics should include root messages, got: %s", out)
	}
}

func TestSpaceUnknownSubcommand(t *testing.T) {
	cmds := NewCommands(newFakeIOAClient(), "tester", nil)
	commands.Output.Reset(nil)
	err := findCmd(t, cmds, "ioa_space").Execute(context.Background(), []string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("expected unknown subcommand error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ioa_send subcommands
// ---------------------------------------------------------------------------

func TestSendBroadcast(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	if err := findCmd(t, cmds, "ioa_send").Execute(context.Background(), []string{
		"--content", `{"content":"hello"}`,
	}); err != nil {
		t.Fatalf("ioa_send: %v", err)
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

	if err := findCmd(t, cmds, "ioa_send").Execute(context.Background(), []string{
		"to", "--node", "target-node-42", "--content", `{"content":"hi"}`,
	}); err != nil {
		t.Fatalf("ioa_send to: %v", err)
	}
	if client.lastSentBody.Refs == nil || len(client.lastSentBody.Refs.Nodes) != 1 || client.lastSentBody.Refs.Nodes[0] != "target-node-42" {
		t.Fatalf("send to node refs = %+v, want nodes=[target-node-42]", client.lastSentBody.Refs)
	}
}

func TestSendToNodeMissingFlag(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	commands.Output.Reset(nil)
	err := findCmd(t, cmds, "ioa_send").Execute(context.Background(), []string{
		"to", "--content", `{"content":"hi"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "--node") {
		t.Fatalf("expected --node required error, got: %v", err)
	}
}

func TestSendReply(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	if err := findCmd(t, cmds, "ioa_send").Execute(context.Background(), []string{
		"reply", "--to", "msg-99", "--content", `{"content":"noted"}`,
	}); err != nil {
		t.Fatalf("ioa_send reply: %v", err)
	}
	if client.lastSentBody.Refs == nil || len(client.lastSentBody.Refs.Messages) != 1 || client.lastSentBody.Refs.Messages[0] != "msg-99" {
		t.Fatalf("reply refs = %+v, want messages=[msg-99]", client.lastSentBody.Refs)
	}
}

func TestSendReplyMissingTo(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	commands.Output.Reset(nil)
	err := findCmd(t, cmds, "ioa_send").Execute(context.Background(), []string{
		"reply", "--content", `{"content":"x"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "--to") {
		t.Fatalf("expected --to required error, got: %v", err)
	}
}

func TestSendWithoutSpace(t *testing.T) {
	cmds := NewCommands(newFakeIOAClient(), "tester", nil)
	commands.Output.Reset(nil)
	err := findCmd(t, cmds, "ioa_send").Execute(context.Background(), []string{
		"--content", `{"content":"hello"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "ioa_space") {
		t.Fatalf("expected space error, got: %v", err)
	}
}

func TestSendWithoutContent(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	commands.Output.Reset(nil)
	err := findCmd(t, cmds, "ioa_send").Execute(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "--content") {
		t.Fatalf("expected content error, got: %v", err)
	}
}

func TestSendUnknownSubcommand(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	commands.Output.Reset(nil)
	err := findCmd(t, cmds, "ioa_send").Execute(context.Background(), []string{
		"bogus", "--content", `{"content":"x"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("expected unknown subcommand error, got: %v", err)
	}
}

func TestSendCheckpoint(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	commands.Output.Reset(nil)
	if err := findCmd(t, cmds, "ioa_send").Execute(context.Background(), []string{
		"checkpoint",
		"--kind", "verify",
		"--title", "SQL Injection Found",
		"--content", "Confirmed via error-based injection on /login",
		"--target", "http://10.0.0.1:8080",
		"--status", "confirmed",
	}); err != nil {
		t.Fatalf("ioa_send checkpoint: %v", err)
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

func TestSendCheckpointMissingArgs(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	commands.Output.Reset(nil)
	err := findCmd(t, cmds, "ioa_send").Execute(context.Background(), []string{
		"checkpoint", "--kind", "verify",
	})
	if err == nil || !strings.Contains(err.Error(), "--title") {
		t.Fatalf("expected --title required error, got: %v", err)
	}
}

func TestSendCheckpointWithoutSpace(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)

	commands.Output.Reset(nil)
	err := findCmd(t, cmds, "ioa_send").Execute(context.Background(), []string{
		"checkpoint", "--kind", "verify", "--title", "test",
	})
	if err == nil || !strings.Contains(err.Error(), "no space joined") {
		t.Fatalf("expected no-space error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// ioa_read subcommands
// ---------------------------------------------------------------------------

func TestReadDefault(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	commands.Output.Reset(nil)
	if err := findCmd(t, cmds, "ioa_read").Execute(context.Background(), nil); err != nil {
		t.Fatalf("ioa_read: %v", err)
	}
	if client.lastReadOpts.All {
		t.Fatal("default read should not set All")
	}
}

func TestReadAll(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	if err := findCmd(t, cmds, "ioa_read").Execute(context.Background(), []string{
		"all", "--limit", "10",
	}); err != nil {
		t.Fatalf("ioa_read all: %v", err)
	}
	if !client.lastReadOpts.All {
		t.Fatal("ioa_read all should set All=true")
	}
	if client.lastReadOpts.Limit != 10 {
		t.Fatalf("limit = %d, want 10", client.lastReadOpts.Limit)
	}
}

func TestReadThread(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	if err := findCmd(t, cmds, "ioa_read").Execute(context.Background(), []string{
		"thread", "--id", "msg-42",
	}); err != nil {
		t.Fatalf("ioa_read thread: %v", err)
	}
	if client.lastReadOpts.MessageID != "msg-42" {
		t.Fatalf("message_id = %q, want msg-42", client.lastReadOpts.MessageID)
	}
}

func TestReadThreadMissingID(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	commands.Output.Reset(nil)
	err := findCmd(t, cmds, "ioa_read").Execute(context.Background(), []string{"thread"})
	if err == nil || !strings.Contains(err.Error(), "--id") {
		t.Fatalf("expected --id required error, got: %v", err)
	}
}

func TestReadNew(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	if err := findCmd(t, cmds, "ioa_read").Execute(context.Background(), []string{
		"new", "--after", "cursor-abc",
	}); err != nil {
		t.Fatalf("ioa_read new: %v", err)
	}
	if client.lastReadOpts.After != "cursor-abc" {
		t.Fatalf("after = %q, want cursor-abc", client.lastReadOpts.After)
	}
}

func TestReadWithoutSpace(t *testing.T) {
	cmds := NewCommands(newFakeIOAClient(), "tester", nil)
	commands.Output.Reset(nil)
	err := findCmd(t, cmds, "ioa_read").Execute(context.Background(), nil)
	if err == nil || !strings.Contains(err.Error(), "ioa_space") {
		t.Fatalf("expected space error, got: %v", err)
	}
}

func TestReadUnknownSubcommand(t *testing.T) {
	client := newFakeIOAClient(protocols.SpaceInfo{ID: knownSpaceID, Name: "my-space"})
	cmds := NewCommands(client, "tester", nil)
	joinSpace(t, cmds)

	commands.Output.Reset(nil)
	err := findCmd(t, cmds, "ioa_read").Execute(context.Background(), []string{"bogus"})
	if err == nil || !strings.Contains(err.Error(), "unknown subcommand") {
		t.Fatalf("expected unknown subcommand error, got: %v", err)
	}
}

// ---------------------------------------------------------------------------
// default space binding
// ---------------------------------------------------------------------------

func TestDefaultSpaceSkipsJoin(t *testing.T) {
	client := newFakeIOAClient()
	cmds := NewCommands(client, "tester", nil)
	findCmd(t, cmds, "ioa_space").(interface{ SetDefaultSpace(string) }).SetDefaultSpace(knownSpaceID)

	if err := findCmd(t, cmds, "ioa_send").Execute(context.Background(), []string{
		"--content", `{"content":"hello"}`,
	}); err != nil {
		t.Fatalf("ioa_send with default space: %v", err)
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
//	go test -v -run TestLLMIOAToolUsage ./pkg/tools/ioa/ -timeout 120s
func TestLLMIOAToolUsage(t *testing.T) {
	apiKey := os.Getenv("LIVE_TEST_API_KEY")
	if apiKey == "" {
		apiKey = os.Getenv("DEEPSEEK_API_KEY")
	}
	if apiKey == "" {
		t.Skip("set LIVE_TEST_API_KEY or DEEPSEEK_API_KEY to run live LLM IOA test")
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

	registry := commands.NewRegistry()
	for _, cmd := range cmds {
		registry.Register(cmd, "ioa")
	}
	dir := t.TempDir()
	bash := commands.NewBashTool(dir, 30)
	bash.Manager().SetCommands(func(name string) (tmuxpkg.Command, bool) {
		return registry.Get(name)
	})
	bash.Manager().SetWorkDir(dir)
	bash.SetCommandNames(registry.Names)
	registry.RegisterTool(bash)
	t.Cleanup(bash.Close)

	systemPrompt := `You are a testing agent. You have IOA tools available as pseudo-commands through the bash tool.

Available pseudo-commands:
` + registry.UsageDocs() + `

IMPORTANT: These pseudo-commands run through the bash tool. Example: bash {"command": "ioa_space join --name test --description agent"}

Your task:
1. First, join the space named "test-space" with description "integration test"
2. Then send a broadcast message with content {"content": "test message from LLM"}
3. Then read all messages from the space
4. Finally, report what you did in plain text.

Execute each step one at a time.`

	t.Logf("System prompt:\n%s", systemPrompt)

	ag := agent.NewAgent(agent.Config{
		Provider: llm,
		Tools:    registry,
		Model:    model,
	}.
		WithSystemPrompt(systemPrompt).
		WithStream(false))

	result, err := ag.Run(context.Background(), "Execute the IOA integration test steps described in your instructions.")
	if err != nil {
		t.Fatalf("agent.Run: %v", err)
	}

	t.Logf("Agent output:\n%s", result.Output)
	t.Logf("Turns: %d, Total tokens: %d", result.Turns, result.TotalUsage.TotalTokens)

	// Verify the LLM actually called the tools
	if len(client.spaceCalls) == 0 {
		t.Error("LLM never called ioa_space join")
	}
	if len(client.sentSpaceIDs) == 0 {
		t.Error("LLM never called ioa_send")
	}
	if len(client.readSpaceIDs) == 0 {
		t.Error("LLM never called ioa_read")
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

func joinSpace(t *testing.T, cmds []commands.Command) {
	t.Helper()
	joinSpaceByName(t, cmds, "my-space")
}

func joinSpaceByName(t *testing.T, cmds []commands.Command, name string) {
	t.Helper()
	commands.Output.Reset(nil)
	if err := findCmd(t, cmds, "ioa_space").Execute(context.Background(), []string{
		"join", "--name", name, "--description", "test",
	}); err != nil {
		t.Fatalf("ioa_space join %s: %v", name, err)
	}
}

func findCmd(t *testing.T, cmds []commands.Command, name string) commands.Command {
	t.Helper()
	for _, cmd := range cmds {
		if cmd.Name() == name {
			return cmd
		}
	}
	t.Fatalf("command %q not found", name)
	return nil
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
