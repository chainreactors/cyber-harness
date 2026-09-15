//go:build live_llm

package harness_test

import (
	"bytes"
	"context"
	"crypto/rand"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func TestLiveLLMMultiAgentIOAThreadAndIsolation(t *testing.T) {
	liveLLMRequest(t)
	w := newWorkspace(t)
	server := w.start(t)
	endpoint := "http://harness-local-access@" + strings.TrimPrefix(server.url, "http://") + "/ioa"
	var challenge [5]byte
	if _, err := rand.Read(challenge[:]); err != nil {
		t.Fatal(err)
	}
	nonce := hex.EncodeToString(challenge[:4])
	word := []string{"alpha", "bravo", "delta"}[int(challenge[4])%3]
	space := "harness-ioa-" + nonce
	isolation := space + "-isolated"
	first := newIOAOperator(t, w, "agent-a", endpoint, space, isolation)
	second := newIOAOperator(t, w, "agent-b", endpoint, space, isolation)
	ctx, cancelOps := context.WithTimeout(t.Context(), 140*time.Second)
	defer cancelOps()
	first.phase(t, ctx, `Join the shared work space and inspect nodes. Publish exactly one root task with text task:`+nonce+`:`+word+`. The format is task:NONCE:WORD. Do not set message or node references on the root. Read it back before ending this phase. Your peer will later uppercase WORD and reply with the unchanged NONCE.`)
	second.phase(t, ctx, `Join the work space and read its messages. Find the task from agent-a. Its text has format task:NONCE:WORD. Send exactly one reply with text result:NONCE:UPPERCASE_WORD. Preserve NONCE exactly and uppercase only WORD. Reference the task message ID and target its sender node. Read the original thread to verify your reply is present before ending this phase. Do not switch spaces yet.`)
	first.phase(t, ctx, `Read the original task thread. A peer should have replied with result:NONCE:UPPERCASE_WORD. Verify both the original nonce and the uppercased word. Only if correct, send exactly one acknowledgement with text ack:NONCE (two fields only; omit task: and WORD), referencing the peer reply message ID and targeting its sender. Read the thread again to verify the acknowledgement, then end this phase.`)
	second.phase(t, ctx, `Read the original thread and verify the acknowledgement from agent-a. Then join the isolation space and read it; it must contain none of the original thread. Publish exactly one root marker with text isolated, read it back, and end this phase while still in the isolation space.`)
	// Oracles read real messages independently of both models' conclusions.
	work := readIOAMessages(t, first.client, "ioa read --all --limit 30")
	root := uniqueIOAMessage(t, work, "task:"+nonce+":"+word)
	result := uniqueIOAMessage(t, work, "result:"+nonce+":"+strings.ToUpper(word))
	ack := uniqueIOAMessage(t, work, "ack:"+nonce)
	if len(work) != 3 || root.Sender == result.Sender || ack.Sender != root.Sender || len(root.Refs.Messages) != 0 || len(root.Refs.Nodes) != 0 {
		t.Fatalf("unexpected work-space conversation: %+v", work)
	}
	assertIOAReply(t, result, root)
	assertIOAReply(t, ack, result)
	thread := readIOAMessages(t, first.client, "ioa read --all --message "+root.ID+" --direction downstream")
	uniqueIOAMessage(t, thread, ack.Content.Text)
	if _, ok := first.seen[result.ID]; !ok {
		t.Fatal("agent A did not observe B's actual reply")
	}
	if _, ok := second.seen[ack.ID]; !ok {
		t.Fatal("agent B did not observe A's actual acknowledgement")
	}
	if !second.isolated {
		t.Fatal("agent B never selected isolation space")
	}
	isolated := readIOAMessages(t, second.client, "ioa read --all --limit 30")
	marker := uniqueIOAMessage(t, isolated, "isolated")
	if len(isolated) != 1 || marker.Sender != result.Sender || marker.SpaceID == root.SpaceID {
		t.Fatalf("space isolation failed: %+v", isolated)
	}
	if _, err := first.client.command(t, "ioa space "+isolation+" harness"); err != nil {
		t.Fatal(err)
	}
	fromPeer := readIOAMessages(t, first.client, "ioa read --all --limit 30")
	if len(fromPeer) != 1 || fromPeer[0].ID != marker.ID {
		t.Fatal("second node could not confirm isolated marker")
	}
	writeEvidence(t, w.dir, map[string]any{"work": work, "thread": thread, "isolated": isolated, "model_requests": []int{first.requests, second.requests}})
}

type ioaMessage struct {
	ID      string `json:"id"`
	SpaceID string `json:"space_id"`
	Sender  string `json:"sender"`
	Content struct {
		Text string `json:"text"`
	} `json:"content"`
	Refs struct {
		Messages []string `json:"messages"`
		Nodes    []string `json:"nodes"`
	} `json:"refs"`
}

func readIOAMessages(t *testing.T, client *stdioClient, line string) []ioaMessage {
	t.Helper()
	out, err := client.command(t, line)
	if err != nil {
		t.Fatal(err)
	}
	var messages []ioaMessage
	if err = json.Unmarshal([]byte(out), &messages); err != nil {
		t.Fatalf("invalid IOA messages: %v: %s", err, out)
	}
	return messages
}
func uniqueIOAMessage(t *testing.T, messages []ioaMessage, text string) ioaMessage {
	t.Helper()
	var found []ioaMessage
	for _, m := range messages {
		if m.Content.Text == text {
			found = append(found, m)
		}
	}
	if len(found) != 1 {
		t.Fatalf("expected one %q message, got %d in %+v", text, len(found), messages)
	}
	if found[0].ID == "" || found[0].Sender == "" || found[0].SpaceID == "" {
		t.Fatalf("missing IOA identity: %+v", found[0])
	}
	return found[0]
}
func assertIOAReply(t *testing.T, reply, parent ioaMessage) {
	t.Helper()
	if reply.SpaceID != parent.SpaceID || len(reply.Refs.Messages) != 1 || reply.Refs.Messages[0] != parent.ID || len(reply.Refs.Nodes) != 1 || reply.Refs.Nodes[0] != parent.Sender {
		t.Fatalf("incorrect reply references: %+v -> %+v", reply, parent)
	}
}
func writeEvidence(t *testing.T, dir string, v any) {
	t.Helper()
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, filepath.Join(dir, "ioa-evidence.json"), redactSecrets(data))
}

type ioaOperation struct {
	Action  string `json:"action"`
	Text    string `json:"text"`
	Message string `json:"message"`
	Node    string `json:"node"`
}

// Each operator owns its own model history and product process. The harness
// admits only IOA operations with inert data, never model-generated shell code.
// This is a model acting as an external user, not the product's Agent loop.
type ioaOperator struct {
	client         *stdioClient
	config         map[string]any
	history        []any
	log            *os.File
	requests       int
	workSpace      string
	isolationSpace string
	sent           int
	read           int
	isolated       bool
	seen           map[string]ioaMessage
}

func newIOAOperator(t *testing.T, w *workspace, name, endpoint, space, isolation string) *ioaOperator {
	t.Helper()
	cfg := liveLLMRequest(t)
	if cfg["provider"] != "openai" && cfg["provider"] != "deepseek" {
		t.Fatal("IOA model operators require an OpenAI-compatible tool-calling provider: openai or deepseek")
	}
	f, err := os.Create(filepath.Join(w.dir, name+"-model.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := f.Close(); err != nil {
			t.Error(err)
		}
	})
	o := &ioaOperator{client: startStdioClient(t, w, name, endpoint, space), config: cfg, log: f, workSpace: space, isolationSpace: isolation, seen: make(map[string]ioaMessage)}
	o.history = []any{map[string]any{"role": "system", "content": `You are an AI participant testing local IOA communication. Use the ioa tool to execute each task. Other participants have separate memories; messages reach them only through IOA. Inspect actual results and use returned IDs, never invent IDs or claim an action without executing it. Available actions: join_work, join_isolation, nodes, read, thread (message ID), send (text, optional message reference and recipient node), done. Leave unused arguments empty. Send text must be inert ASCII letters, numbers, spaces, underscores, colons or hyphens, at most 160 characters. Submit exactly one tool call per response. Call done only after completing the current phase; put your concise evidence-based conclusion in text.`}}
	return o
}

func (o *ioaOperator) record(t *testing.T, v any) {
	t.Helper()
	data, err := json.Marshal(v)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = o.log.Write(append(redactSecrets(data), '\n')); err != nil {
		t.Fatal(err)
	}
}

func (o *ioaOperator) phase(t *testing.T, ctx context.Context, task string) {
	t.Helper()
	o.history = append(o.history, map[string]any{"role": "user", "content": task})
	o.record(t, map[string]any{"task": task})
	actions := []string{"join_work", "join_isolation", "nodes", "read", "thread", "send", "done"}
	properties := map[string]any{"action": map[string]any{"type": "string", "enum": actions}}
	for _, name := range []string{"text", "message", "node"} {
		properties[name] = map[string]any{"type": "string"}
	}
	tool := map[string]any{"type": "function", "function": map[string]any{"name": "ioa", "description": "Perform one IOA operation through your own local product process.", "parameters": map[string]any{"type": "object", "properties": properties, "required": []string{"action", "text", "message", "node"}, "additionalProperties": false}}}
	httpClient := &http.Client{Timeout: 30 * time.Second, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	for o.requests < 24 {
		o.requests++
		body, err := json.Marshal(map[string]any{"model": o.config["model"], "messages": o.history, "max_tokens": 512, "tools": []any{tool}, "tool_choice": map[string]any{"type": "function", "function": map[string]any{"name": "ioa"}}})
		if err != nil {
			t.Fatal(err)
		}
		req, err := http.NewRequestWithContext(ctx, http.MethodPost, strings.TrimRight(o.config["baseUrl"].(string), "/")+"/chat/completions", bytes.NewReader(body))
		if err != nil {
			t.Fatal(err)
		}
		req.Header.Set("Authorization", "Bearer "+o.config["apiKey"].(string))
		req.Header.Set("Content-Type", "application/json")
		resp, err := httpClient.Do(req)
		if err != nil {
			t.Fatalf("model request: %s", redactSecrets([]byte(err.Error())))
		}
		data, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
		resp.Body.Close()
		if err != nil {
			t.Fatal(err)
		}
		if resp.StatusCode != 200 {
			t.Fatalf("model HTTP %d: %s", resp.StatusCode, redactSecrets(data))
		}
		var completion struct {
			Choices []struct {
				Message json.RawMessage `json:"message"`
			} `json:"choices"`
			Usage any `json:"usage"`
		}
		if err := json.Unmarshal(data, &completion); err != nil || len(completion.Choices) != 1 {
			t.Fatal("invalid model completion")
		}
		o.record(t, map[string]any{"request": o.requests, "completion": completion})
		var message struct {
			Calls []struct {
				ID       string `json:"id"`
				Function struct {
					Name      string `json:"name"`
					Arguments string `json:"arguments"`
				} `json:"function"`
			} `json:"tool_calls"`
		}
		if err := json.Unmarshal(completion.Choices[0].Message, &message); err != nil || len(message.Calls) != 1 {
			t.Fatal("model must issue exactly one IOA call")
		}
		call := message.Calls[0]
		var op ioaOperation
		if call.Function.Name != "ioa" {
			t.Fatal("model called an unknown tool")
		}
		if err := json.Unmarshal([]byte(call.Function.Arguments), &op); err != nil {
			t.Fatal("malformed IOA arguments")
		}
		out, err := o.operate(t, op)
		if err != nil {
			out += "\nOPERATION_ERROR: " + err.Error()
		}
		o.record(t, map[string]any{"operation": op, "observation": out})
		o.history = append(o.history, completion.Choices[0].Message, map[string]any{"role": "tool", "tool_call_id": call.ID, "content": out})
		t.Logf("%s request %d: %s", filepath.Base(o.log.Name()), o.requests, op.Action)
		if op.Action == "done" && err == nil {
			return
		}
	}
	t.Fatal("IOA operator exhausted its total budget of 24 model requests")
}

var inertIOAText = regexp.MustCompile(`^[A-Za-z0-9 _:-]{1,160}$`)
var ioaIdentifier = regexp.MustCompile(`^[A-Za-z0-9_-]{1,80}$`)

func (o *ioaOperator) operate(t *testing.T, op ioaOperation) (string, error) {
	t.Helper()
	var command string
	switch op.Action {
	case "done":
		return "Phase ended; harness will independently verify the messages.", nil
	case "join_work":
		command = "ioa space " + o.workSpace + " harness"
	case "join_isolation":
		command = "ioa space " + o.isolationSpace + " harness"
	case "nodes":
		command = "ioa space nodes"
	case "read":
		command = "ioa read --all --limit 30"
		o.read++
	case "thread":
		if !ioaIdentifier.MatchString(op.Message) {
			return "", fmt.Errorf("thread requires an observed message ID")
		}
		command = "ioa read --all --message " + op.Message + " --direction downstream"
	case "send":
		if !inertIOAText.MatchString(op.Text) {
			return "", fmt.Errorf("send requires 1-160 inert ASCII characters")
		}
		data, _ := json.Marshal(map[string]string{"text": op.Text})
		command = "ioa send --content '" + string(data) + "'"
		if op.Message != "" {
			if !ioaIdentifier.MatchString(op.Message) {
				return "", fmt.Errorf("invalid message ID")
			}
			command += " --ref-messages " + op.Message
		}
		if op.Node != "" {
			if !ioaIdentifier.MatchString(op.Node) {
				return "", fmt.Errorf("invalid node ID")
			}
			command += " --ref-nodes " + op.Node
		}
	default:
		return "", fmt.Errorf("unknown IOA operation")
	}
	out, err := o.client.command(t, command)
	if err == nil {
		if op.Action == "send" {
			o.sent++
		}
		if op.Action == "join_isolation" {
			o.isolated = true
		}
		if op.Action == "join_work" {
			o.isolated = false
		}
		if op.Action == "read" || op.Action == "thread" {
			var messages []ioaMessage
			if decodeErr := json.Unmarshal([]byte(out), &messages); decodeErr != nil {
				return out, decodeErr
			}
			for _, message := range messages {
				o.seen[message.ID] = message
			}
		}
	}
	return out, err
}
