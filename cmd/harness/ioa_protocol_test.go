package harness_test

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

type ioaMessage struct {
	ContentType string         `json:"content_type,omitempty"`
	Meta        map[string]any `json:"meta,omitempty"`
	ID          string         `json:"id"`
	SpaceID     string         `json:"space_id"`
	Sender      string         `json:"sender"`
	Content     struct {
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
func assertIOAReply(t *testing.T, reply, parent ioaMessage) {
	t.Helper()
	if reply.SpaceID != parent.SpaceID || len(reply.Refs.Messages) != 1 || reply.Refs.Messages[0] != parent.ID || len(reply.Refs.Nodes) != 1 || reply.Refs.Nodes[0] != parent.Sender {
		t.Fatalf("incorrect reply references: %+v -> %+v", reply, parent)
	}
}

// writeEvidence records one scenario's acceptance evidence, redacted.
func writeEvidence(t *testing.T, path string, value any) {
	t.Helper()
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		t.Fatal(err)
	}
	writeFile(t, path, redactSecrets(data))
}

// These tests exercise the shipped executable and actual IOA stores, without
// claiming model participation. Closed targets must not wake another Session.
func TestUserIOAMemoryCommandRoundTrip(t *testing.T) {
	w := newWorkspace(t)
	p := startStdioClient(t, w, "memory", "", "memory-work", stdioAgentMode{commandOnly: true})
	closeIOATarget(t, p)
	if _, err := p.command(t, "ioa space memory-work harness"); err != nil {
		t.Fatal(err)
	}
	first := sendIOAFromProcess(t, p, `ioa send --content '{"text":"memory-root"}'`)
	reply := sendIOAFromProcess(t, p, `ioa send --content '{"text":"memory-reply"}' --target-session closed-session --ref-messages `+first.ID)
	assertClosedIOATarget(t, filepath.Join(w.dir, "memory", "stderr.log"))
	history := readIOAMessages(t, p, "ioa read --all --limit 20")
	if len(history) != 2 || reply.Sender != first.Sender || len(reply.Refs.Messages) != 1 || reply.Refs.Messages[0] != first.ID || len(reply.Refs.Nodes) != 1 || reply.Refs.Nodes[0] != first.Sender {
		t.Fatalf("invalid memory trace: %+v", history)
	}
	if _, err := p.command(t, "ioa space isolated harness"); err != nil {
		t.Fatal(err)
	}
	if got := readIOAMessages(t, p, "ioa read --all --limit 20"); len(got) != 0 {
		t.Fatalf("space leaked: %+v", got)
	}
	if _, err := p.command(t, "ioa space memory-work harness"); err != nil {
		t.Fatal(err)
	}
	if got := readIOAMessages(t, p, "ioa read --all --limit 20"); len(got) != 2 {
		t.Fatalf("history lost: %+v", got)
	}
	writeEvidence(t, filepath.Join(w.dir, "memory-ioa-evidence.json"), history)
}

func TestUserIOAExternalProcessesRoundTrip(t *testing.T) {
	w := newWorkspace(t)
	server := w.start(t)
	endpoint := "http://harness-local-access@" + strings.TrimPrefix(server.url, "http://") + "/ioa"
	a := startStdioClient(t, w, "a", endpoint, "process-work", stdioAgentMode{commandOnly: true})
	closeIOATarget(t, a)
	if _, err := a.command(t, "ioa space process-work harness"); err != nil {
		t.Fatal(err)
	}
	first := sendIOAFromProcess(t, a, `ioa send --content '{"text":"external-root"}'`)
	b := startStdioClient(t, w, "b", endpoint, "process-work", stdioAgentMode{commandOnly: true})
	closeIOATarget(t, b)
	if _, err := b.command(t, "ioa space process-work harness"); err != nil {
		t.Fatal(err)
	}
	if got := readIOAMessages(t, b, "ioa read --all --limit 20"); len(got) != 1 || got[0].ID != first.ID {
		t.Fatalf("late join cannot read prior context: %+v", got)
	}
	reply := sendIOAFromProcess(t, b, `ioa send --content '{"text":"external-reply"}' --target-session closed-session --ref-nodes `+first.Sender+` --ref-messages `+first.ID)
	ack := sendIOAFromProcess(t, a, `ioa send --content '{"text":"external-ack"}' --target-session closed-session --ref-nodes `+reply.Sender+` --ref-messages `+reply.ID)
	assertClosedIOATarget(t, filepath.Join(w.dir, "a", "stderr.log"))
	assertClosedIOATarget(t, filepath.Join(w.dir, "b", "stderr.log"))
	if first.Sender == reply.Sender || ack.Sender != first.Sender {
		t.Fatal("independent processes shared an identity")
	}
	assertIOAReply(t, reply, first)
	assertIOAReply(t, ack, reply)
	history := readIOAMessages(t, b, "ioa read --all --limit 20")
	if len(history) != 3 {
		t.Fatalf("invalid shared trace: %+v", history)
	}
	writeEvidence(t, filepath.Join(w.dir, "external-ioa-evidence.json"), history)
}

func closeIOATarget(t *testing.T, p *stdioClient) {
	t.Helper()
	for _, action := range []string{"open", "close"} {
		response := p.request(t, "aop.ProtocolMessage", action+"SessionRequest", map[string]any{"sessionId": "closed-session"})
		if field(response, action+"SessionResponse", "accepted") == nil {
			t.Fatalf("%s target Session rejected: %v", action, response)
		}
	}
}

func assertClosedIOATarget(t *testing.T, logPath string) {
	t.Helper()
	deadline := time.Now().Add(3 * time.Second)
	for time.Now().Before(deadline) {
		data, err := os.ReadFile(logPath)
		if err != nil {
			t.Fatal(err)
		}
		if strings.Contains(string(data), `no active IOA receiver for session "closed-session"`) {
			return
		}
		time.Sleep(20 * time.Millisecond)
	}
	t.Fatalf("closed Session delivery was not visibly rejected; see %s", logPath)
}

func sendIOAFromProcess(t *testing.T, p *stdioClient, line string) ioaMessage {
	t.Helper()
	output, err := p.command(t, line)
	if err != nil {
		t.Fatal(err)
	}
	var message ioaMessage
	if err = json.Unmarshal([]byte(output), &message); err != nil {
		t.Fatalf("send response: %v", err)
	}
	if message.ID == "" || message.Sender == "" {
		t.Fatal("missing saved-message identity")
	}
	var raw map[string]any
	if err = json.Unmarshal([]byte(output), &raw); err != nil {
		t.Fatal(err)
	}
	if field(raw, "meta", "source_session_id") != "operator" {
		t.Fatalf("missing invocation provenance: %v", raw)
	}
	return message
}
