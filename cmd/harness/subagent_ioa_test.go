//go:build live_llm

package harness_test

import (
	"crypto/rand"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestLiveLLMParentDelegatesIOASiblings(t *testing.T) {
	cfg := liveLLMRequest(t)
	w := newWorkspace(t)
	server := w.start(t)
	var seed [4]byte
	if _, err := rand.Read(seed[:]); err != nil {
		t.Fatal(err)
	}
	nonce := hex.EncodeToString(seed[:])
	space := "harness-siblings-" + nonce
	gate := newSubagentGateway(t, w.dir, space, nonce)
	endpoint := "http://harness-local-access@" + strings.TrimPrefix(server.url, "http://") + "/ioa"
	p := startStdioClient(t, w, "parent", endpoint, space, stdioAgentMode{providerURL: gate.server.URL, model: cfg["model"].(string)})
	// Subtasks are given to the parent as user instructions. Only the real
	// parent's subagent tool may create them; the harness sends one root turn.
	common := `Use only bash to invoke IOA. Complete by returning a final text response without tool calls. The current work space is already joined; do not switch spaces. Use exactly these command forms:
ioa read --all --limit 20
ioa read --all --message ID --direction downstream
ioa send --content '{"text":"TEXT"}'
ioa send --content '{"text":"TEXT"}' --ref-messages ID
Replace ID with the observed message id. Set bash timeout=10, omit wait. No shell operators, files, external hosts or other tools. Each read returns real server state; poll again if a peer message is not present yet. All siblings share one IOA node, so identify the thread by message IDs, not by distinct senders. Do not use subagent.message. Use exactly one send per required message.`
	jobA := "HARNESS_A\n" + common + "\nSend offer:" + nonce + " as a root message, then read until a reply:" + nonce + " references your offer. Send ack:" + nonce + " referencing that reply. Read back the ack and finish with summary A_DONE:" + nonce + "."
	jobB := "HARNESS_B\n" + common + "\nRead until a root offer:NONCE is present. Discover NONCE only from that message. Send reply:NONCE referencing the offer. Read until ack:NONCE references your reply, then finish with summary B_DONE:NONCE. Preserve the nonce exactly."
	prompt := "HARNESS_PARENT\nRun this local communication acceptance task. First use bash with timeout=10 to run exactly: ioa space " + space + " harness\nThen create exactly two async subagents named worker-a and worker-b (type empty), using the following respective task prompts, including their HARNESS_A/HARNESS_B markers. Dispatch both before waiting. You must call the real subagent tool. Do not do their IOA messaging yourself, do not relay messages through subagent.message, and never include worker-a's nonce in worker-b's prompt.\nWorker A prompt:\n" + jobA + "\nWorker B prompt:\n" + jobB + "\nAfter dispatching both, output a brief waiting message without tool calls so the runtime can wait for subagent completions. Once BOTH actual completion notifications have arrived, verify A_DONE and B_DONE, read the IOA thread using bash command ioa read --all --limit 20, and return final text PARENT_DONE:" + nonce + " without tool calls. Only bash and subagent create/list are available."
	r := p.request(t, "aop.ProtocolMessage", "runTurnRequest", map[string]any{"sessionId": "operator", "turnId": "delegation-task", "maxTurns": 16, "input": map[string]any{"role": "user", "content": []any{map[string]any{"text": map[string]any{"text": prompt}}}}})
	if field(r, "runTurnResponse", "accepted") == nil {
		t.Fatalf("parent turn rejected: %v", r)
	}
	deadline := time.NewTimer(150 * time.Second)
	defer deadline.Stop()
	ticker := time.NewTicker(100 * time.Millisecond)
	defer ticker.Stop()
waitParent:
	for {
		for _, event := range p.eventSnapshot() {
			if event["sessionId"] == "operator" && event["turnId"] == "delegation-task" && event["turnEnded"] != nil {
				if field(event, "turnEnded", "error") != nil {
					t.Fatalf("parent failed: %v", event)
				}
				break waitParent
			}
		}
		select {
		case err := <-gate.failure:
			t.Fatalf("model boundary: %v", err)
		case <-p.done:
			t.Fatalf("product ended: %v", p.err)
		case <-deadline.C:
			t.Fatal("parent/subagent IOA task exceeded 150s")
		case <-ticker.C:
		}
	}
	events := p.eventSnapshot()
	work := readIOAMessages(t, p, "ioa read --all --limit 20")
	offer := uniqueIOAMessage(t, work, "offer:"+nonce)
	reply := uniqueIOAMessage(t, work, "reply:"+nonce)
	ack := uniqueIOAMessage(t, work, "ack:"+nonce)
	if len(work) != 3 || len(offer.Refs.Messages) != 0 {
		t.Fatalf("unexpected sibling conversation: %+v", work)
	}
	for _, pair := range [][2]ioaMessage{{reply, offer}, {ack, reply}} {
		child, parent := pair[0], pair[1]
		if child.SpaceID != parent.SpaceID || len(child.Refs.Messages) != 1 || child.Refs.Messages[0] != parent.ID || child.Sender != parent.Sender {
			t.Fatalf("invalid shared-node IOA reply: %+v", child)
		}
	}
	proof := assertSiblingEvents(t, events, offer, reply, ack, nonce)
	// Automatic handoff recording is another product path, separate from the
	// siblings' own IOA messages. Read it before closing the runtime.
	if _, err := p.command(t, "ioa space "+space+"-inbox-parent harness"); err != nil {
		t.Fatal(err)
	}
	handoffs := waitSiblingHandoffs(t, p, proof)
	gate.mu.Lock()
	counts := make(map[string]int)
	for role, count := range gate.roles {
		counts[role] = count
	}
	notifications := gate.parentCompletions["worker-a"] && gate.parentCompletions["worker-b"]
	gate.mu.Unlock()
	if !notifications || counts["parent"] == 0 || counts["a"] == 0 || counts["b"] == 0 {
		t.Fatalf("missing actual model participation or completions: %v", counts)
	}
	select {
	case err := <-gate.failure:
		t.Fatal(err)
	default:
	}
	writeJSONEvidence(t, filepath.Join(w.dir, "subagent-evidence.json"), map[string]any{"sessions": proof, "work": work, "handoffs": handoffs, "model_requests": counts, "shared_ioa_node": offer.Sender})
	t.Logf("parent and two real subagents completed IOA exchange; model requests: %v", counts)
}

type siblingSession struct {
	Session, ParentCall string
	Started, Ended      int
}

func assertSiblingEvents(t *testing.T, events []map[string]any, offer, reply, ack ioaMessage, nonce string) map[string]siblingSession {
	t.Helper()
	proof := map[string]siblingSession{}
	spawns := map[string]string{}
	calls := map[string]map[string]any{}
	callSessions := map[string]string{}
	sent := map[string]string{}
	observed := map[string]map[string]bool{}
	parentEnded := -1
	finalText := map[string]string{}
	for index, event := range events {
		session, _ := event["sessionId"].(string)
		if start, ok := event["sessionStarted"].(map[string]any); ok && start["parentSessionId"] != nil {
			if start["parentSessionId"] != "operator" {
				t.Fatalf("unexpected nested delegation: %v", event)
			}
			name, _ := event["emitter"].(string)
			if name != "worker-a" && name != "worker-b" {
				t.Fatalf("unexpected child %q", name)
			}
			if _, exists := proof[name]; exists {
				t.Fatalf("duplicate child %q", name)
			}
			parentCall, _ := start["parentToolCallId"].(string)
			proof[name] = siblingSession{session, parentCall, index, -1}
		}
		if call, ok := event["toolCall"].(map[string]any); ok {
			id, _ := call["id"].(string)
			calls[id] = call
			callSessions[id] = session
			args := decodeEventArguments(t, call)
			if call["name"] == "subagent" && (args["action"] == nil || args["action"] == "" || args["action"] == "create") {
				if session != "operator" {
					t.Fatal("child created a subagent")
				}
				name, _ := args["name"].(string)
				spawns[name] = id
			}
		}
		if message, ok := event["message"].(map[string]any); ok && message["role"] == "assistant" {
			if text := eventOutputText(message["content"]); text != "" {
				finalText[session] = text
			}
		}
		if end, ok := event["turnEnded"].(map[string]any); ok && session == "operator" {
			if end["stopReason"] != "completed" {
				t.Fatalf("parent did not complete normally: %v", end)
			}
			parentEnded = index
		}
		if result, ok := event["toolResult"].(map[string]any); ok {
			if result["isError"] == true {
				t.Fatalf("tool failed: %v", event)
			}
			id, _ := result["callId"].(string)
			call := calls[id]
			if call == nil || callSessions[id] != session {
				t.Fatalf("unmatched tool result %s", id)
			}
			if call["name"] == "bash" {
				command, _ := decodeEventArguments(t, call)["command"].(string)
				output := eventOutputText(result["output"])
				if strings.HasPrefix(command, "ioa send ") {
					var message ioaMessage
					if err := json.Unmarshal([]byte(output), &message); err != nil {
						t.Fatalf("send output: %s: %v", output, err)
					}
					sent[message.ID] = session
				}
				if strings.HasPrefix(command, "ioa read ") {
					var messages []ioaMessage
					if err := json.Unmarshal([]byte(output), &messages); err != nil {
						t.Fatalf("read output: %v", err)
					}
					if observed[session] == nil {
						observed[session] = map[string]bool{}
					}
					for _, message := range messages {
						observed[session][message.ID] = true
					}
				}
			}
		}
		if end, ok := event["sessionEnded"].(map[string]any); ok {
			for name, child := range proof {
				if child.Session == session {
					if end["reason"] != "terminated" && end["reason"] != "completed" {
						t.Fatalf("child did not complete: %v", event)
					}
					child.Ended = index
					proof[name] = child
				}
			}
		}
	}
	if len(proof) != 2 || len(spawns) != 2 || parentEnded < 0 || !strings.Contains(finalText["operator"], "PARENT_DONE:"+nonce) {
		t.Fatalf("missing delegation or final completion: %+v / %+v", proof, spawns)
	}
	a, b := proof["worker-a"], proof["worker-b"]
	if parentEnded <= a.Ended || parentEnded <= b.Ended || !strings.Contains(finalText[a.Session], "A_DONE:"+nonce) || !strings.Contains(finalText[b.Session], "B_DONE:"+nonce) {
		t.Fatal("missing child final results before parent completion")
	}
	for name, child := range proof {
		if child.ParentCall == "" || child.ParentCall != spawns[name] || child.Session == "operator" || child.Ended <= child.Started {
			t.Fatalf("invalid child provenance: %s %+v", name, child)
		}
	}
	if a.Session == b.Session || a.Started >= b.Ended || b.Started >= a.Ended {
		t.Fatal("siblings were not distinct overlapping async sessions")
	}
	if sent[offer.ID] != a.Session || sent[reply.ID] != b.Session || sent[ack.ID] != a.Session {
		t.Fatalf("IOA messages did not originate from the correct subagents: %+v", sent)
	}
	if !observed[a.Session][reply.ID] || !observed[b.Session][offer.ID] || !observed[b.Session][ack.ID] || !observed["operator"][ack.ID] {
		t.Fatal("missing peer reads or parent verification of the IOA exchange")
	}
	return proof
}

func decodeEventArguments(t *testing.T, call map[string]any) map[string]any {
	t.Helper()
	encoded, _ := field(call, "arguments", "data").(string)
	data, err := base64.StdEncoding.DecodeString(encoded)
	if err != nil {
		t.Fatal(err)
	}
	var args map[string]any
	if err := json.Unmarshal(data, &args); err != nil {
		t.Fatal(err)
	}
	return args
}
func eventOutputText(value any) string {
	var out strings.Builder
	items, _ := value.([]any)
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			if s, ok := field(m, "text", "text").(string); ok {
				out.WriteString(s)
			}
		}
	}
	return out.String()
}
func writeJSONEvidence(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, redactSecrets(data))
}

func waitSiblingHandoffs(t *testing.T, p *stdioClient, proof map[string]siblingSession) []map[string]any {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for {
		out, err := p.command(t, "ioa read --all --limit 20")
		if err != nil {
			t.Fatal(err)
		}
		var messages []map[string]any
		if err := json.Unmarshal([]byte(out), &messages); err != nil {
			t.Fatal(err)
		}
		if len(messages) == 4 {
			for name, child := range proof {
				var delegate, returned map[string]any
				for _, message := range messages {
					meta, _ := field(message, "meta", "subagent").(map[string]any)
					if meta["name"] != name {
						continue
					}
					if meta["session_id"] != child.Session || meta["parent_tool_call_id"] != child.ParentCall || meta["parent_session_id"] != "operator" || meta["mode"] != "async" || message["content_type"] != "handoff" {
						t.Fatalf("handoff identity mismatch: %v", message)
					}
					switch meta["phase"] {
					case "delegate":
						delegate = message
					case "return":
						if meta["status"] != "completed" {
							t.Fatalf("unsuccessful child handoff: %v", message)
						}
						returned = message
					}
				}
				if delegate == nil || returned == nil {
					t.Fatalf("missing delegation/return for %s", name)
				}
				refs, _ := field(returned, "refs", "messages").([]any)
				if len(refs) != 1 || refs[0] != delegate["id"] {
					t.Fatalf("return does not reference delegate: %v", returned)
				}
			}
			return messages
		}
		if time.Now().After(deadline) {
			t.Fatalf("expected four recorded handoffs, got %d: %s", len(messages), out)
		}
		time.Sleep(100 * time.Millisecond)
	}
}
