//go:build live_llm

package harness_test

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

// stdioClient speaks the public JSONL protocol to a separately built executable.
// No application implementation or transport helper is imported by the harness.
type stdioClient struct {
	input   io.WriteCloser
	replies chan map[string]any
	done    chan struct{}
	err     error // read only after done
	seq     int
	eventMu sync.Mutex
	events  []map[string]any
}

type stdioAgentMode struct{ providerURL, model string }

func startStdioClient(t *testing.T, w *workspace, name, ioaURL, space string, modes ...stdioAgentMode) *stdioClient {
	t.Helper()
	dir := filepath.Join(w.dir, name)
	if err := os.MkdirAll(dir, 0700); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 4*time.Minute)
	args := []string{"--config", w.config, "--data-dir", filepath.Join(dir, "data"), "--no-color",
		"agent", "--transport", "stdio", "--timeout", "220", "--ioa-url", ioaURL, "--node-name", name, "--space", space + "-inbox-" + name}
	internalAgent := len(modes) == 1
	if len(modes) > 1 {
		cancel()
		t.Fatal("one stdio agent mode expected")
	}
	if internalAgent {
		if !strings.HasPrefix(modes[0].providerURL, "http://127.0.0.1:") {
			cancel()
			t.Fatal("internal Agent requires the local bounded model gateway")
		}
		args = append(args, "--provider", "openai", "--base-url", modes[0].providerURL, "--model", modes[0].model, "--api-key", "harness-gateway-token", "--max-tokens", "1024")
	}
	cmd := exec.CommandContext(ctx, executablePath, args...)
	cmd.Dir, cmd.Env = dir, testEnvironment(!internalAgent)
	p := &stdioClient{replies: make(chan map[string]any, 16), done: make(chan struct{})}
	errLog := newLog(t, filepath.Join(dir, "stderr.log"))
	trace := newLog(t, filepath.Join(dir, "protocol.jsonl"))
	cmd.Stderr = errLog
	input, err := cmd.StdinPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	p.input = input
	output, err := cmd.StdoutPipe()
	if err != nil {
		cancel()
		t.Fatal(err)
	}
	configureProcess(cmd)
	if err := cmd.Start(); err != nil {
		cancel()
		t.Fatal(err)
	}
	go func() {
		scanner := bufio.NewScanner(output)
		scanner.Buffer(make([]byte, 4096), 4<<20)
		for scanner.Scan() {
			line := append([]byte(nil), scanner.Bytes()...)
			if _, err := trace.Write(append(line, '\n')); err != nil {
				p.err = err
				cancel()
				break
			}
			var envelope map[string]any
			if err := json.Unmarshal(line, &envelope); err != nil {
				p.err = fmt.Errorf("invalid application JSONL: %w", err)
				cancel()
				break
			}
			// Manual IOA membership and the runtime's startup inbox are separate.
			// These operators explicitly poll the work space, while each application
			// subscribes to its own empty inbox. Unexpected internal turns fail.
			if !internalAgent && field(envelope, "payload", "event", "turnStarted") != nil {
				p.err = fmt.Errorf("unexpected application Agent turn in external-operator scenario")
				cancel()
				break
			}
			if internalAgent {
				if event, ok := field(envelope, "payload", "event").(map[string]any); ok {
					for _, kind := range []string{"sessionStarted", "sessionEnded", "turnStarted", "turnEnded", "toolCall", "toolResult", "message", "error"} {
						if event[kind] != nil {
							p.eventMu.Lock()
							p.events = append(p.events, event)
							p.eventMu.Unlock()
							break
						}
					}
				}
			}
			if correlation, _ := envelope["replyTo"].(string); correlation != "" {
				select {
				case p.replies <- envelope:
				case <-ctx.Done():
				}
			}
		}
		p.err = errors.Join(p.err, scanner.Err(), cmd.Wait(), trace.Close(), errLog.Close())
		close(p.done)
	}()
	t.Cleanup(func() {
		_ = p.input.Close()
		select {
		case <-p.done:
		case <-time.After(10 * time.Second):
			cancel()
			<-p.done
			t.Errorf("stdio EOF did not release %s", name)
		}
		cancel()
		if p.err != nil {
			t.Errorf("stdio application %s: %v; see %s", name, p.err, dir)
		}
	})
	response := p.request(t, "aop.ProtocolMessage", "openSessionRequest", map[string]any{"sessionId": "operator"})
	if field(response, "openSessionResponse", "accepted") == nil {
		t.Fatalf("session rejected: %v", response)
	}
	return p
}

func (p *stdioClient) eventSnapshot() []map[string]any {
	p.eventMu.Lock()
	defer p.eventMu.Unlock()
	return append([]map[string]any(nil), p.events...)
}

func (p *stdioClient) request(t *testing.T, namespace, operation string, value any) map[string]any {
	t.Helper()
	p.seq++
	id := fmt.Sprintf("request-%d", p.seq)
	message := map[string]any{"id": id, "payload": map[string]any{"@type": "type.googleapis.com/" + namespace, operation: value}}
	if err := json.NewEncoder(p.input).Encode(message); err != nil {
		t.Fatal(err)
	}
	select {
	case response := <-p.replies:
		if response["replyTo"] != id {
			t.Fatalf("wrong correlation: got %v want %s", response["replyTo"], id)
		}
		payload, ok := response["payload"].(map[string]any)
		if !ok {
			t.Fatal("missing protocol payload")
		}
		return payload
	case <-p.done:
		t.Fatalf("application exited before %s: %v", operation, p.err)
	case <-time.After(35 * time.Second):
		t.Fatalf("application did not answer %s", operation)
	}
	return nil
}

func (p *stdioClient) command(t *testing.T, line string) (string, error) {
	t.Helper()
	response := p.request(t, "cyber.command.CommandProtocolMessage", "request", map[string]any{"sessionId": "operator", "line": "!" + line})
	if failure := field(response, "protocolError"); failure != nil {
		return "", fmt.Errorf("%v", failure)
	}
	items, ok := field(response, "result", "content").([]any)
	if !ok {
		return "", fmt.Errorf("missing command output: %v", response)
	}
	var out strings.Builder
	for _, item := range items {
		if m, ok := item.(map[string]any); ok {
			if s, ok := field(m, "text", "text").(string); ok {
				out.WriteString(s)
			}
		}
	}
	return out.String(), nil
}
