package harness_test

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"
)

type gatewayRequest struct {
	Model    string `json:"model"`
	Stream   bool   `json:"stream"`
	Messages []struct {
		Role    string `json:"role"`
		Content any    `json:"content"`
	} `json:"messages"`
}

func TestUserAgentOneShotFormatsSkillAndResume(t *testing.T) {
	w := newWorkspace(t)
	const (
		apiKey      = "harness-secret-must-not-leak"
		skillMarker = "LOCAL_SKILL_MARKER_9f74"
	)

	var (
		requestsMu sync.Mutex
		requests   []gatewayRequest
	)
	gateway := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/chat/completions" {
			http.NotFound(w, r)
			return
		}
		if got := r.Header.Get("Authorization"); got != "Bearer "+apiKey {
			http.Error(w, "bad authorization", http.StatusUnauthorized)
			return
		}
		var request gatewayRequest
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), http.StatusBadRequest)
			return
		}
		body, _ := json.Marshal(request)
		if bytes.Contains(body, []byte(apiKey)) {
			http.Error(w, "API key leaked into model request body", http.StatusBadRequest)
			return
		}
		requestsMu.Lock()
		requests = append(requests, request)
		sequence := len(requests)
		requestsMu.Unlock()

		reply := fmt.Sprintf("fixture-answer-%d", sequence)
		if request.Stream {
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprintf(w, "data: {\"choices\":[{\"delta\":{\"role\":\"assistant\",\"content\":%q},\"index\":0}]}\n\n", reply)
			fmt.Fprintln(w, `data: {"choices":[{"delta":{},"finish_reason":"stop","index":0}],"usage":{"prompt_tokens":11,"completion_tokens":3,"total_tokens":14}}`)
			fmt.Fprintln(w)
			fmt.Fprintln(w, "data: [DONE]")
			return
		}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{
			"choices": []any{map[string]any{
				"message":       map[string]any{"role": "assistant", "content": reply},
				"finish_reason": "stop",
			}},
			"usage": map[string]any{"prompt_tokens": 11, "completion_tokens": 3, "total_tokens": 14},
		})
	}))
	t.Cleanup(gateway.Close)

	skillPath := filepath.Join(w.dir, "local-review.md")
	writeFile(t, skillPath, []byte("---\nname: local-review\ndescription: local CLI fixture\n---\nRequire marker "+skillMarker+" in this task.\n"))
	historyPath := filepath.Join(w.dir, "first-run.jsonl")
	continuedPath := filepath.Join(w.dir, "continued-run.jsonl")

	type invocation struct {
		name   string
		format string
		prompt string
		extra  []string
	}
	invocations := []invocation{
		{name: "text", format: "text", prompt: "text format prompt"},
		{name: "json-and-history", format: "json", prompt: "persist this turn", extra: []string{"--output", historyPath}},
		{name: "stream-json", format: "stream-json", prompt: "stream this turn"},
		{name: "local-skill", format: "json", prompt: "apply the selected local skill", extra: []string{"--skill", skillPath}},
		{name: "resume", format: "json", prompt: "continue with prior context", extra: []string{"--resume", historyPath, "--output", continuedPath}},
	}

	for _, invocation := range invocations {
		t.Run(invocation.name, func(t *testing.T) {
			args := []string{
				"--config", w.config, "--data-dir", filepath.Join(w.dir, "data"), "--no-color",
				"agent", "--provider", "openai", "--base-url", gateway.URL + "/v1",
				"--api-key", apiKey, "--model", "fixture", "--timeout", "30", "--quiet",
				"--output-format", invocation.format, "--prompt", invocation.prompt,
			}
			args = append(args, invocation.extra...)
			ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
			defer cancel()
			cmd := exec.CommandContext(ctx, executablePath, args...)
			cmd.Dir = w.dir
			cmd.Env = testEnvironment(false)
			var stdout, stderr bytes.Buffer
			cmd.Stdout, cmd.Stderr = &stdout, &stderr
			if err := cmd.Run(); err != nil {
				t.Fatalf("one-shot agent: %v\nstdout:\n%s\nstderr:\n%s", err, stdout.String(), stderr.String())
			}
			if bytes.Contains(stdout.Bytes(), []byte(apiKey)) || bytes.Contains(stderr.Bytes(), []byte(apiKey)) {
				t.Fatal("API key leaked into process output")
			}
			switch invocation.format {
			case "text":
				if !strings.Contains(stdout.String(), "fixture-answer-") {
					t.Fatalf("text output has no final answer: %q", stdout.String())
				}
			case "json":
				var result struct {
					Type    string `json:"type"`
					IsError bool   `json:"is_error"`
					Result  string `json:"result"`
				}
				if err := json.Unmarshal(bytes.TrimSpace(stdout.Bytes()), &result); err != nil {
					t.Fatalf("JSON output is not one strict document: %v\n%s", err, stdout.String())
				}
				if result.Type != "result" || result.IsError || !strings.HasPrefix(result.Result, "fixture-answer-") {
					t.Fatalf("unexpected JSON result: %+v", result)
				}
			case "stream-json":
				assertStrictJSONLines(t, stdout.Bytes(), "stream-json stdout")
			}
		})
	}

	for _, path := range []string{historyPath, continuedPath} {
		data, err := os.ReadFile(path)
		if err != nil {
			t.Fatalf("read event output %s: %v", path, err)
		}
		if bytes.Contains(data, []byte(apiKey)) {
			t.Fatalf("API key leaked into %s", path)
		}
		assertStrictJSONLines(t, data, filepath.Base(path))
	}

	requestsMu.Lock()
	captured := append([]gatewayRequest(nil), requests...)
	requestsMu.Unlock()
	if len(captured) != 2*len(invocations) {
		t.Fatalf("gateway requests = %d, want %d startup checks plus %d tasks", len(captured), len(invocations), len(invocations))
	}
	for i, request := range captured {
		if request.Model != "fixture" {
			t.Fatalf("request %d model=%q stream=%v", i+1, request.Model, request.Stream)
		}
	}
	tasks := make([]gatewayRequest, len(invocations))
	for i, invocation := range invocations {
		for _, request := range captured {
			if strings.Contains(lastMessageText(request), invocation.prompt) {
				if len(tasks[i].Messages) != 0 {
					t.Fatalf("multiple task requests contain %q", invocation.prompt)
				}
				tasks[i] = request
			}
		}
		if len(tasks[i].Messages) == 0 || !tasks[i].Stream {
			t.Fatalf("missing streaming task request for %q", invocation.prompt)
		}
	}
	if body := requestText(tasks[3]); !strings.Contains(body, skillMarker) {
		t.Fatalf("local skill content did not reach the provider request:\n%s", body)
	}
	resumed := requestText(tasks[4])
	for _, want := range []string{"persist this turn", "fixture-answer-", "continue with prior context"} {
		if !strings.Contains(resumed, want) {
			t.Fatalf("resumed request is missing %q:\n%s", want, resumed)
		}
	}
}

func requestText(request gatewayRequest) string {
	data, _ := json.Marshal(request.Messages)
	return string(data)
}

func lastMessageText(request gatewayRequest) string {
	if len(request.Messages) == 0 {
		return ""
	}
	data, _ := json.Marshal(request.Messages[len(request.Messages)-1])
	return string(data)
}

func assertStrictJSONLines(t *testing.T, data []byte, label string) {
	t.Helper()
	scanner := bufio.NewScanner(bytes.NewReader(data))
	lines := 0
	for scanner.Scan() {
		line := bytes.TrimSpace(scanner.Bytes())
		if len(line) == 0 {
			continue
		}
		var value map[string]any
		if err := json.Unmarshal(line, &value); err != nil {
			t.Fatalf("%s line %d is not JSON: %v\n%s", label, lines+1, err, line)
		}
		lines++
	}
	if err := scanner.Err(); err != nil && err != io.EOF {
		t.Fatalf("scan %s: %v", label, err)
	}
	if lines == 0 {
		t.Fatalf("%s has no JSON records", label)
	}
}
