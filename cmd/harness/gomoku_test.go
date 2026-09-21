//go:build live_llm

package harness_test

import (
	"context"
	"embed"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

//go:embed gomoku-task.md
var gomokuTask embed.FS

func TestLiveLLMIOAGomoku(t *testing.T) {
	cfg := liveLLMRequest(t)
	w := newWorkspace(t)
	w.processTimeout = 35 * time.Minute
	project := filepath.Join(w.dir, "project")
	if err := os.MkdirAll(project, 0700); err != nil {
		t.Fatal(err)
	}
	root, err := repositoryRoot()
	if err != nil {
		t.Fatal(err)
	}
	binary := filepath.Join(artifactRoot, "neutralhost.exe")
	buildCtx, cancelBuild := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancelBuild()
	build := exec.CommandContext(buildCtx, "go", "build", "-o", binary, "./cmd/harness/testdata/neutralhost")
	build.Dir = root
	if out, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build generic host: %v: %s", err, out)
	}
	t.Cleanup(func() { _ = os.Remove(binary) })
	server := w.start(t)
	endpoint := "http://harness-local-access@" + strings.TrimPrefix(server.url, "http://") + "/ioa"
	space := "gomoku-" + fmt.Sprint(time.Now().UnixNano())
	evidence := map[string]any{"started_at": time.Now().UTC(), "workspace": w.dir, "model": cfg["model"], "space": space}
	var gate *subagentGateway
	// Register before gateway/client cleanup so final counts include cancellation
	// and shutdown, rather than racing in-flight requests during a failed run.
	t.Cleanup(func() {
		if gate == nil {
			return
		}
		gate.mu.Lock()
		defer gate.mu.Unlock()
		evidence["finished_at"], evidence["model_requests"], evidence["output_tokens_charged"], evidence["requests_by_session"] = time.Now().UTC(), gate.requests, gate.outputTokens, gate.roles
		evidence["execution_completed"] = !t.Failed()
		evidence["requests_missing_usage"] = gate.usageMissing
		writeEvidence(t, filepath.Join(artifactRoot, "gomoku-evidence.json"), evidence)
	})
	gate = startSubagentGateway(t, w.dir, space, "", true)
	gate.gameTask = true
	p := startStdioClient(t, w, "players", endpoint, space, stdioAgentMode{sessionID: "black", executable: binary, providerURL: gate.server.URL, model: cfg["model"].(string), longTask: true, workDir: project, environment: []string{"PYTHONUTF8=1"}})
	t.Cleanup(func() {
		select {
		case <-p.done:
			return
		default:
		}
		for _, id := range []string{"black", "white"} {
			p.request(t, "aop.ProtocolMessage", "closeSessionRequest", map[string]any{"sessionId": id})
		}
	})
	response := p.request(t, "aop.ProtocolMessage", "openSessionRequest", map[string]any{"sessionId": "white"})
	if field(response, "openSessionResponse", "accepted") == nil {
		t.Fatal(response)
	}
	task, _ := gomokuTask.ReadFile("gomoku-task.md")
	for _, id := range []string{"white", "black"} {
		other, color := "white", "黑方（先手）"
		if id == "white" {
			other, color = "black", "白方（后手）"
		}
		prompt := string(task) + fmt.Sprintf("\n你是%s，Session ID 是 %s，对手是 %s。通过 `ioa send %s \"消息\"` 通讯，已在同一空间。\n工作目录：%s。辅助文件统一用 %s_ 前缀；你的棋谱文件是 %s_game.md。黑方另交付 game.html。\n", color, id, other, other, filepath.ToSlash(project), id, id)
		writeFile(t, filepath.Join(w.dir, id+"-task.md"), []byte(prompt))
		response := p.request(t, "aop.ProtocolMessage", "runTurnRequest", map[string]any{"sessionId": id, "turnId": id + "-game", "maxTurns": 100, "input": map[string]any{"role": "user", "content": []any{map[string]any{"text": map[string]any{"text": prompt}}}}})
		if field(response, "runTurnResponse", "accepted") == nil {
			t.Fatal(response)
		}
	}
	deadline := time.NewTimer(30 * time.Minute)
	defer deadline.Stop()
	tick := time.NewTicker(time.Second)
	defer tick.Stop()
	progress := time.NewTicker(20 * time.Second)
	defer progress.Stop()
	completed := make(map[string]bool)
	for {
		for _, event := range p.eventSnapshot() {
			id, _ := event["sessionId"].(string)
			if (id == "black" || id == "white") && event["turnId"] == id+"-game" && event["turnEnded"] != nil {
				if field(event, "turnEnded", "stopReason") != "completed" {
					writeEvidence(t, filepath.Join(w.dir, "ioa-history.json"), longHistory(t, p))
					t.Fatalf("game execution ended: %v", event)
				}
				completed[id] = true
			}
		}
		if len(completed) == 2 {
			writeEvidence(t, filepath.Join(w.dir, "ioa-history.json"), longHistory(t, p))
			for _, name := range []string{"black_game.md", "white_game.md", "game.html"} {
				if _, err := os.Stat(filepath.Join(project, name)); err != nil {
					t.Error(err)
				}
			}
			t.Logf("game execution ended; inspect actual moves and outcome: %s", project)
			return
		}
		select {
		case err := <-gate.failure:
			t.Fatal(err)
		case <-p.done:
			t.Fatalf("host exited: %v", p.err)
		case <-deadline.C:
			t.Fatal("30 minute game deadline")
		case <-tick.C:
		case <-progress.C:
			gate.mu.Lock()
			t.Logf("model_requests=%d charged_output=%d sessions=%v", gate.requests, gate.outputTokens, gate.roles)
			gate.mu.Unlock()
		}
	}
}
