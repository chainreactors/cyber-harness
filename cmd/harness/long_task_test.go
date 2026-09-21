//go:build live_llm

package harness_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"regexp"
	"strings"
	"testing"
	"time"
)

func longTaskRole(body map[string]any) string {
	messages, _ := body["messages"].([]any)
	for i := len(messages) - 1; i >= 0; i-- {
		m, ok := messages[i].(map[string]any)
		if !ok || m["role"] != "user" {
			continue
		}
		text := strings.TrimSpace(chatText(m["content"]))
		for _, role := range []string{"coordinator", "analyst", "validator", "repairer", "reviewer"} {
			if strings.HasPrefix(text, "ORDERLAB_ROLE="+role+"\n") {
				return role
			}
		}
	}
	return ""
}

func validateLongTaskCalls(role string, calls []gatewayCall, projectRoot string) error {
	for _, call := range calls {
		var args map[string]any
		if err := json.Unmarshal([]byte(call.Function.Arguments), &args); err != nil {
			return err
		}
		switch call.Function.Name {
		case "read", "ls", "glob", "write":
			path, _ := args["path"].(string)
			// These are application-owned, read-only help resources advertised by
			// the tools themselves, not private harness evidence.
			if call.Function.Name == "read" && (strings.HasPrefix(path, "cyber://skills/") || strings.HasPrefix(path, "ioa://skills/")) && !strings.Contains(path, "..") {
				continue
			}
			if strings.Contains(path, "://") {
				return fmt.Errorf("file tools are limited to the project")
			}
			if !filepath.IsAbs(path) {
				path = filepath.Join(projectRoot, path)
			}
			rel, err := filepath.Rel(projectRoot, filepath.Clean(path))
			if err != nil || rel == ".." || strings.HasPrefix(rel, ".."+string(filepath.Separator)) {
				return fmt.Errorf("file tools are limited to the project")
			}
			if call.Function.Name == "write" {
				allowed := rel == "policy.go" || rel == "report.md" || rel == "results.json"
				if filepath.Dir(rel) == "." && strings.HasSuffix(rel, "_test.go") && rel != "policy_test.go" {
					allowed = true
				}
				if !allowed {
					return fmt.Errorf("write to protected project file %q", rel)
				}
			}
		case "subagent":
			if role != "coordinator" {
				return fmt.Errorf("only coordinator may delegate")
			}
			action, _ := args["action"].(string)
			if action == "list" || action == "kill" {
				continue
			}
			if action != "" && action != "create" {
				return fmt.Errorf("invalid delegation action")
			}
			prompt, _ := args["prompt"].(string)
			childRole := longTaskRole(map[string]any{"messages": []any{map[string]any{"role": "user", "content": prompt}}})
			if childRole == "" || childRole == "coordinator" || len(prompt) > 16000 {
				return fmt.Errorf("child task needs ORDERLAB_ROLE=analyst|validator|repairer|reviewer header")
			}
			if typ, _ := args["name"].(string); typ != "" {
				return fmt.Errorf("default child type required")
			}
		case "bash":
			command, _ := args["command"].(string)
			if strings.TrimSpace(command) == "" || len(command) > 32000 {
				return fmt.Errorf("invalid command")
			}
			if n, ok := args["timeout"].(float64); ok && (n <= 0 || n > 120) {
				return fmt.Errorf("command timeout must be at most 120 seconds")
			}
			for _, forbidden := range []string{"subagent-model.jsonl", "protocol.jsonl", "http-audit.jsonl", "long-task-evidence.json", "orderLabReferencePolicy", "CYBER_HARNESS_LLM"} {
				if strings.Contains(command, forbidden) {
					return fmt.Errorf("command reads private harness evidence")
				}
			}
		default:
			return fmt.Errorf("tool %q is outside local task tools", call.Function.Name)
		}
	}
	return nil
}

func responseOutputTokens(data []byte, stream bool) (int, bool) {
	frames := []string{string(data)}
	if stream {
		frames = strings.Split(string(data), "\n")
	}
	for _, frame := range frames {
		if stream {
			if !strings.HasPrefix(frame, "data:") {
				continue
			}
			frame = strings.TrimSpace(strings.TrimPrefix(frame, "data:"))
		}
		var v map[string]any
		if json.Unmarshal([]byte(frame), &v) != nil {
			continue
		}
		if n, ok := field(v, "usage", "completion_tokens").(float64); ok {
			return int(n), true
		}
	}
	return 0, false
}

const longTaskInstructions = `You are running inside the real application Agent loop. Use the existing bash, workspace file and subagent tools, with actual model decisions and IOA communication. Do not invent HTTP responses, request IDs, IOA IDs, test results or completions.
Every delegated prompt MUST begin with ORDERLAB_ROLE=analyst, ORDERLAB_ROLE=validator, ORDERLAB_ROLE=repairer or ORDERLAB_ROLE=reviewer followed by a newline. Only the coordinator may spawn children; omit name for anonymous subagents. At most three children may run concurrently. Use async for overlapping investigation, at least one fork to inherit scope, and a bounded sync review where helpful. Give children the project path. Do not copy the audit batch marker into the fork prompt: it must be inherited. A child ends when it produces its final answer, so if you need its peer reply, keep its task running until received. Keep waiting bounded and count it as work.
All task assignments after initial dispatch, evidence, corrections and handover notes go through IOA. Initial delegate and final return are recorded automatically: do not manually duplicate them. Address live peers with ioa send --target-session SESSION (same Node) and reference prior messages with --ref-messages ID. Read the actual Session IDs from subagent outputs/list or delegate records. Messages arrive in peer Inbox with source_session_id and message_id. Send success means saved, not consumed. At least one pair of overlapping investigation children must exchange actionable findings/evidence through their peer Inbox. They must do real follow-up work using that evidence, not just acknowledge a code word. Never address completed children: create a new task referencing their record.
Use actual shell commands, files, go tests, and python dev.py HTTP requests. Only edit policy.go, add new *_test.go files, and write report.md/results.json inside the project. Do not edit fixtures, initial tests, credentials, database, development scripts or harness files. Do not read previous model/protocol/server-audit logs. No external business targets. Keep commands bounded to 120 seconds. Shell cwd may reset; use explicit cd to the project for each command. One code writer at a time. Read README.md for the business rules, development commands and output schema.
The coordinator must observe actual subagent_completion inputs before declaring investigation complete. Record scope, observations, changed decisions, source revision and unfinished work in IOA, with real evidence references. A later coordinator will receive only the objective, project path and IOA space. It must be able to continue from the records.
`

func TestLiveLLMIOALongTask(t *testing.T) {
	status := "blocked_missing_model_configuration"
	evidence := map[string]any{"status": status, "started_at": time.Now().UTC(), "live_model_executed": false}
	t.Cleanup(func() {
		evidence["stage"] = status
		if t.Failed() {
			status = "failed"
		}
		evidence["status"] = status
		evidence["finished_at"] = time.Now().UTC()
		writeEvidence(t, filepath.Join(artifactRoot, "long-task-evidence.json"), evidence)
	})
	cfg := liveLLMRequest(t)
	status = "preparing"
	w := newWorkspace(t)
	w.processTimeout = 65 * time.Minute
	lab := setupOrderLab(t, w.dir)
	backend := os.Getenv("CYBER_HARNESS_LONG_BACKEND")
	if backend == "" {
		backend = "external"
	}
	if backend != "external" && backend != "memory" {
		t.Fatal("CYBER_HARNESS_LONG_BACKEND must be external or memory")
	}
	evidence["backend"], evidence["workspace"], evidence["baseline_revision"] = backend, w.dir, lab.baseline
	t.Cleanup(func() { evidence["http_requests"] = len(readAudit(t, lab.audit)) })
	endpoint := ""
	if backend == "external" {
		server := w.start(t)
		endpoint = "http://harness-local-access@" + strings.TrimPrefix(server.url, "http://") + "/ioa"
	}
	batch := "batch-" + filepath.Base(w.dir)
	space := "long-task-" + fmt.Sprint(time.Now().UnixNano())
	gate := startSubagentGateway(t, w.dir, space, batch, true)
	mode := stdioAgentMode{providerURL: gate.server.URL, model: cfg["model"].(string), longTask: true, workDir: lab.dir, environment: lab.env}
	p := startStdioClient(t, w, "coordinator", endpoint, space, mode)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Minute)
	defer cancel()
	status = "investigation_running"
	evidence["live_model_executed"] = true
	t.Cleanup(func() {
		gate.mu.Lock()
		defer gate.mu.Unlock()
		evidence["model_requests"] = gate.requests
		evidence["output_tokens_charged"] = gate.outputTokens
		evidence["responses_missing_usage"] = gate.usageMissing
		evidence["requests_by_role"] = gate.roles
	})
	prompt := "ORDERLAB_ROLE=coordinator\n" + longTaskInstructions + fmt.Sprintf("\nProject: %s\nBusiness target: %s\nAudit batch marker (inherit in fork): %s\nIOA space: %s\n", filepath.ToSlash(lab.dir), lab.url, batch, space) +
		"Join the IOA space using ioa space NAME harness. Independently investigate this multi-tenant order/export service before release. Divide source/interface analysis and HTTP verification across children, confirm real access-control defects and reject false positives, preserve positive controls. Do not edit policy.go yet: first establish baseline evidence, including actual order and private export access checks. Save evidence (HTTP request_id, status, body_sha256, revision) and scope in IOA. Wait for your investigation children to finish; then end this turn with INVESTIGATION_READY. A subsequent business requirement will arrive before repairs."
	runLongTaskPhase(t, ctx, p, gate, lab, "investigation", prompt)
	if lab.revision(t) != lab.baseline {
		t.Fatal("baseline modified before requirement change")
	}
	assertLongBaseline(t, lab)
	firstHistory := longHistory(t, p)
	writeEvidence(t, filepath.Join(w.dir, "ioa-after-investigation.json"), firstHistory)
	status = "repair_running"
	requirement := "ORDERLAB_ROLE=coordinator\nBusiness requirement update: Existing exports must be checked against CURRENT access rights at download time. After a user loses order-read permission, previously generated download URLs must stop working. Existing exports must remain usable for authorized owners and tenant administrators. Update the plan and IOA records, reproduce this behavior before modifying code, implement the repairs in policy.go with new regression tests, actually run tests and rebuild/restart the service. Before final independent acceptance, you will hand over to a new coordinator. Save a concise IOA handover with scope, prior message references, current source revision, completed work, unresolved items and the remaining independent verification. Do not write the final results.json/report.md yet; leave substantive final HTTP and regression verification to the successor. Wait for actual repair completion, then end this turn with HANDOVER_READY."
	writeEvidence(t, filepath.Join(w.dir, "requirement-change.json"), map[string]any{"input": requirement, "at": time.Now().UTC()})
	runLongTaskPhase(t, ctx, p, gate, lab, "repair", requirement)
	lab.unchanged(t)
	if lab.revision(t) == lab.baseline {
		t.Fatal("no business policy repair was delivered")
	}
	if _, err := os.Stat(filepath.Join(lab.dir, "results.json")); err == nil {
		t.Fatal("final acceptance must be left to the successor")
	}
	history := longHistory(t, p)
	writeEvidence(t, filepath.Join(w.dir, "ioa-before-handover.json"), history)
	closeResponse := p.request(t, "aop.ProtocolMessage", "closeSessionRequest", map[string]any{"sessionId": "operator", "reason": "completed"})
	if field(closeResponse, "closeSessionResponse", "accepted") == nil {
		t.Fatalf("old Session close failed: %v", closeResponse)
	}
	priorEvents := p.eventSnapshot()
	if backend == "external" {
		_ = p.input.Close()
		select {
		case <-p.done:
			if p.err != nil {
				t.Fatal(p.err)
			}
		case <-time.After(15 * time.Second):
			t.Fatal("old application did not exit before handover")
		}
		p = startStdioClient(t, w, "successor", endpoint, space, mode)
	} else {
		response := p.request(t, "aop.ProtocolMessage", "openSessionRequest", map[string]any{"sessionId": "operator"})
		if field(response, "openSessionResponse", "accepted") == nil {
			t.Fatalf("fresh Session rejected: %v", response)
		}
	}
	status = "successor_running"
	handoverAt := time.Now().UTC()
	evidence["handover_at"] = handoverAt
	successor := fmt.Sprintf("ORDERLAB_ROLE=coordinator\n%s\nYou are the new coordinator. Project: %s. IOA space: %s. Objective: finish release access-control remediation and independently verify the delivered order/export service, preserving normal operations. The former coordinator has stopped. Join the space and read its history to recover the current requirements, evidence, decisions, revision and unfinished work. Do not read old local conversations or private audit logs. Delegate a fresh independent review, perform remaining HTTP/regression checks yourself or through children, and write report.md plus results.json per the project README. Correlate your new evidence with prior IOA records; save final conclusions to IOA. Report uncertainty honestly and then finish with RELEASE_REVIEW_COMPLETE.", longTaskInstructions, filepath.ToSlash(lab.dir), space)
	writeEvidence(t, filepath.Join(w.dir, "successor-input.json"), map[string]any{"input": successor})
	runLongTaskPhase(t, ctx, p, gate, lab, "successor", successor)
	lab.unchanged(t)
	status = "independent_verification"
	history = longHistory(t, p)
	writeEvidence(t, filepath.Join(w.dir, "ioa-final.json"), history)
	assertLongReport(t, lab, history, handoverAt)
	events := p.eventSnapshot()
	if backend == "external" {
		events = append(priorEvents, events...)
	}
	assertLongCollaboration(t, events, history, filepath.Join(w.dir, "subagent-model.jsonl"), batch, backend)
	newTests, err := filepath.Glob(filepath.Join(lab.dir, "*_test.go"))
	if err != nil || len(newTests) < 2 {
		t.Fatal("no new regression tests delivered")
	}
	commandCtx, stop := context.WithTimeout(ctx, 2*time.Minute)
	defer stop()
	cmd := exec.CommandContext(commandCtx, "go", "test", "-mod=mod", "-count=1", "./...")
	cmd.Dir = lab.dir
	cmd.Env = lab.env
	raw, err := cmd.CombinedOutput()
	writeFile(t, filepath.Join(w.dir, "candidate-tests.log"), redactSecrets(raw))
	if err != nil {
		t.Fatalf("candidate regression tests failed: %v", err)
	}
	verifyDir := filepath.Join(w.dir, "independent-check")
	checker := setupOrderLab(t, verifyDir)
	writeFile(t, filepath.Join(checker.dir, "policy.go"), readFile(t, filepath.Join(lab.dir, "policy.go")))
	if _, err = checker.dev("restart"); err != nil {
		t.Fatal(err)
	}
	checkOrderLab(t, checker, true)
	evidence["final_revision"] = lab.revision(t)
	evidence["http_requests"] = len(readAudit(t, lab.audit))
	evidence["ioa_messages"] = len(history)
	status = "passed"
	t.Logf("real model long task passed; artifacts: %s", w.dir)
}

func runLongTaskPhase(t *testing.T, ctx context.Context, p *stdioClient, g *subagentGateway, l *orderLab, id, prompt string) {
	t.Helper()
	response := p.request(t, "aop.ProtocolMessage", "runTurnRequest", map[string]any{"sessionId": "operator", "turnId": id, "maxTurns": 80, "input": map[string]any{"role": "user", "content": []any{map[string]any{"text": map[string]any{"text": prompt}}}}})
	if field(response, "runTurnResponse", "accepted") == nil {
		t.Fatalf("phase %s rejected: %v", id, response)
	}
	ticker := time.NewTicker(250 * time.Millisecond)
	defer ticker.Stop()
	progress := time.NewTicker(20 * time.Second)
	defer progress.Stop()
	for {
		active, peak := 0, 0
		finished := false
		for _, event := range p.eventSnapshot() {
			if field(event, "sessionStarted", "parentSessionId") != nil {
				active++
				if active > peak {
					peak = active
				}
			}
			if event["sessionEnded"] != nil && event["sessionId"] != "operator" {
				active--
			}
			if event["sessionId"] == "operator" && event["turnId"] == id && event["turnEnded"] != nil {
				if field(event, "turnEnded", "error") != nil || field(event, "turnEnded", "stopReason") != "completed" {
					t.Fatalf("phase %s ended unsuccessfully: %v", id, event)
				}
				finished = true
			}
		}
		if peak > 3 {
			t.Fatal("more than three concurrent child tasks")
		}
		if finished {
			if len(readAudit(t, l.audit)) > 600 {
				t.Fatal("600 HTTP request budget exceeded")
			}
			select {
			case err := <-g.failure:
				t.Fatal(err)
			default:
			}
			return
		}
		select {
		case err := <-g.failure:
			t.Fatalf("model gateway: %v", err)
		case <-p.done:
			t.Fatalf("application exited during %s: %v", id, p.err)
		case <-ctx.Done():
			t.Fatalf("long task deadline: %v", ctx.Err())
		case <-ticker.C:
		case <-progress.C:
			g.mu.Lock()
			requests, tokens := g.requests, g.outputTokens
			g.mu.Unlock()
			rows := readAudit(t, l.audit)
			if len(rows) > 600 {
				t.Fatal("600 HTTP request budget exceeded")
			}
			t.Logf("phase=%s model_requests=%d output_tokens_charged=%d http_requests=%d active_children=%d", id, requests, tokens, len(rows), active)
		}
	}
}

func longHistory(t *testing.T, p *stdioClient) []map[string]any {
	t.Helper()
	out, err := p.command(t, "ioa read --all --limit 500")
	if err != nil {
		t.Fatal(err)
	}
	var rows []map[string]any
	if err = json.Unmarshal([]byte(out), &rows); err != nil {
		t.Fatal(err)
	}
	return rows
}

func assertLongBaseline(t *testing.T, l *orderLab) {
	t.Helper()
	orders, exports, fault, recovered := false, false, false, false
	faultPath := ""
	for _, r := range readAudit(t, l.audit) {
		path, _ := r["path"].(string)
		if r["revision"] != l.baseline {
			continue
		}
		if r["status"] == float64(200) {
			if findingRequest("order-isolation", r) {
				orders = true
			}
			if findingRequest("export-isolation", r) {
				exports = true
			}
			if path == faultPath && fault {
				recovered = true
			}
		}
		if r["status"] == float64(503) {
			fault = true
			faultPath = path
		}
	}
	if !orders || !exports || !fault || !recovered {
		t.Fatalf("baseline actual HTTP evidence incomplete: orders=%v exports=%v injected503=%v recovered=%v", orders, exports, fault, recovered)
	}
}

func assertLongReport(t *testing.T, l *orderLab, history []map[string]any, handover time.Time) {
	t.Helper()
	report := readFile(t, filepath.Join(l.dir, "report.md"))
	if len(report) < 100 {
		t.Fatal("missing substantive report")
	}
	var result struct {
		Revision string `json:"revision"`
		Findings []struct {
			ID, Status    string
			Before, After []map[string]any
			IOAMessages   []string `json:"ioa_messages"`
		} `json:"findings"`
		Positive  []map[string]any `json:"positive_controls"`
		Remaining []any            `json:"remaining"`
	}
	if err := json.Unmarshal(readFile(t, filepath.Join(l.dir, "results.json")), &result); err != nil {
		t.Fatal(err)
	}
	if result.Revision != l.revision(t) {
		t.Fatal("report is for a stale source revision")
	}
	audit := map[string]map[string]any{}
	for _, r := range readAudit(t, l.audit) {
		id, _ := r["request_id"].(string)
		audit[id] = r
	}
	ioas := map[string]bool{}
	for _, r := range history {
		if id, ok := r["id"].(string); ok {
			ioas[id] = true
		}
	}
	seen := map[string]bool{}
	check := func(ref map[string]any, current bool) map[string]any {
		t.Helper()
		id, _ := ref["request_id"].(string)
		actual := audit[id]
		if actual == nil {
			t.Fatalf("invented HTTP request %q", id)
		}
		for _, key := range []string{"status", "body_sha256", "revision"} {
			if ref[key] != actual[key] {
				t.Fatalf("HTTP evidence mismatch %s %s", id, key)
			}
		}
		if current {
			at, _ := time.Parse(time.RFC3339Nano, fmt.Sprint(actual["at"]))
			if actual["revision"] != result.Revision || at.Before(handover) {
				t.Fatalf("final evidence predates successor or current code: %s", id)
			}
		}
		return actual
	}
	for _, finding := range result.Findings {
		if seen[finding.ID] {
			t.Fatal("duplicate finding")
		}
		seen[finding.ID] = true
		if finding.Status != "fixed" || len(finding.Before) == 0 || len(finding.After) == 0 || len(finding.IOAMessages) == 0 {
			t.Fatalf("unverified finding: %s", finding.ID)
		}
		for _, id := range finding.IOAMessages {
			if !ioas[id] {
				t.Fatalf("invented IOA reference %s", id)
			}
		}
		for _, ref := range finding.Before {
			r := check(ref, false)
			if r["status"] != float64(200) || !findingRequest(finding.ID, r) || r["revision"] == result.Revision {
				t.Fatal("pre-fix exploit must have succeeded")
			}
		}
		for _, ref := range finding.After {
			r := check(ref, true)
			if r["status"] != float64(403) || !findingRequest(finding.ID, r) {
				t.Fatal("fixed access check must deny unauthorized request")
			}
		}
	}
	for _, id := range []string{"order-isolation", "export-isolation", "export-revocation"} {
		if !seen[id] {
			t.Fatalf("missing result %s", id)
		}
	}
	if len(result.Positive) < 3 {
		t.Fatal("missing normal-operation evidence")
	}
	positiveKinds := map[string]bool{}
	for _, ref := range result.Positive {
		r := check(ref, true)
		if r["status"] != float64(200) {
			t.Fatal("positive control did not succeed")
		}
		path, _ := r["path"].(string)
		if r["tenant"] == r["order_tenant"] && r["can_read"] == true {
			if strings.HasPrefix(path, "/orders/") {
				positiveKinds["order"] = true
			}
			if strings.HasSuffix(path, "/download") && r["export_owner"] == r["actor"] {
				positiveKinds["owner_export"] = true
			}
			if strings.HasSuffix(path, "/download") && r["role"] == "admin" {
				positiveKinds["admin_export"] = true
			}
		}
	}
	if len(positiveKinds) != 3 {
		t.Fatal("positive controls must cover own order, owner export and tenant admin export")
	}
	if len(result.Remaining) > 0 {
		t.Fatalf("task reports unfinished work: %v", result.Remaining)
	}
}

func findingRequest(id string, r map[string]any) bool {
	path, _ := r["path"].(string)
	if r["method"] != "GET" || r["actor"] == "" || r["order_id"] == "" {
		return false
	}
	switch id {
	case "order-isolation":
		return strings.HasPrefix(path, "/orders/") && !strings.HasSuffix(path, "/summary") && r["tenant"] != r["order_tenant"]
	case "export-isolation":
		return strings.HasSuffix(path, "/download") && r["can_read"] == true && (r["tenant"] != r["order_tenant"] || r["export_owner"] != r["actor"] && r["role"] != "admin")
	case "export-revocation":
		return strings.HasSuffix(path, "/download") && r["can_read"] == false && r["export_owner"] == r["actor"] && r["tenant"] == r["order_tenant"]
	}
	return false
}

func assertLongCollaboration(t *testing.T, events, history []map[string]any, modelLog, batch, backend string) {
	t.Helper()
	children := map[string]map[string]any{}
	spawns := map[string]map[string]any{}
	modes := map[string]bool{}
	ended := map[string]bool{}
	for _, event := range events {
		sid, _ := event["sessionId"].(string)
		if start, ok := event["sessionStarted"].(map[string]any); ok && start["parentSessionId"] != nil {
			children[sid] = start
		}
		if event["sessionEnded"] != nil {
			ended[sid] = true
		}
		if call, ok := event["toolCall"].(map[string]any); ok && call["name"] == "subagent" {
			args := decodeEventArguments(t, call)
			if args["action"] == nil || args["action"] == "" || args["action"] == "create" {
				spawns[fmt.Sprint(call["id"])] = args
				modes[fmt.Sprint(args["mode"])] = true
			}
		}
	}
	if len(children) < 3 || !modes["fork"] || !modes["async"] {
		t.Fatalf("missing real delegation coverage: children=%d modes=%v", len(children), modes)
	}
	delegates, returns := map[string]map[string]any{}, map[string]map[string]any{}
	nodes := map[string]bool{}
	peerIDs := map[string]string{}
	for _, msg := range history {
		nodes[fmt.Sprint(msg["sender"])] = true
		meta, _ := msg["meta"].(map[string]any)
		if sub, ok := meta["subagent"].(map[string]any); ok && msg["content_type"] == "handoff" {
			sid := fmt.Sprint(sub["session_id"])
			if sub["phase"] == "delegate" {
				delegates[sid] = msg
			} else if sub["phase"] == "return" {
				returns[sid] = msg
			}
		} else {
			source, target := fmt.Sprint(meta["source_session_id"]), fmt.Sprint(meta["target_session_id"])
			if children[source] != nil && children[target] != nil && source != target {
				peerIDs[fmt.Sprint(msg["id"])] = target
			}
		}
	}
	for sid, start := range children {
		delegate, returned := delegates[sid], returns[sid]
		callID := fmt.Sprint(start["parentToolCallId"])
		if !ended[sid] || delegate == nil || returned == nil || spawns[callID] == nil {
			t.Fatalf("incomplete task trace for %s", sid)
		}
		refs, _ := field(returned, "refs", "messages").([]any)
		if len(refs) != 1 || refs[0] != delegate["id"] {
			t.Fatalf("invalid return reference for %s", sid)
		}
	}
	if backend == "external" && len(nodes) < 2 {
		t.Fatal("successor did not use a distinct IOA Node")
	}
	peerSeen, forkInherited, completionSeen, successorRead := false, false, false, false
	for _, row := range readAudit(t, modelLog) {
		body, ok := row["body"].(map[string]any)
		if !ok || row["request"] == nil {
			continue
		}
		role := fmt.Sprint(row["role"])
		sid := longRequestSession(body)
		if child := children[sid]; child != nil && requestContains(body, batch) {
			args := spawns[fmt.Sprint(child["parentToolCallId"])]
			if args["mode"] == "fork" && !strings.Contains(fmt.Sprint(args["prompt"]), batch) {
				forkInherited = true
			}
		}
		for id, target := range peerIDs {
			if sid == target && requestPeerContains(body, id) {
				peerSeen = true
			}
		}
		if role == "coordinator" && requestCompletionContains(body, "<subagent_completion ") {
			completionSeen = true
		}
		if role == "coordinator" && requestContains(body, "You are the new coordinator.") {
			for _, msg := range history {
				if requestContains(body, fmt.Sprint(msg["id"])) {
					successorRead = true
					break
				}
			}
		}
	}
	if !peerSeen || !forkInherited || !completionSeen || !successorRead {
		t.Fatalf("missing model-input evidence: peer=%v fork=%v completions=%v successor_history=%v", peerSeen, forkInherited, completionSeen, successorRead)
	}
}

var longSessionPattern = regexp.MustCompile(`IOA node=\S+ session=(\S+) space=`)

func longRequestSession(body map[string]any) string {
	messages, _ := body["messages"].([]any)
	for i := len(messages) - 1; i >= 0; i-- {
		m, ok := messages[i].(map[string]any)
		if !ok || m["role"] != "system" {
			continue
		}
		matches := longSessionPattern.FindAllStringSubmatch(chatText(m["content"]), -1)
		if len(matches) > 0 {
			// Fork can retain the parent's identity; the current hook appends
			// the actual child identity at the end of the system message.
			return matches[len(matches)-1][1]
		}
	}
	return ""
}

func TestLongTaskEvidenceClassification(t *testing.T) {
	base := map[string]any{"method": "GET", "path": "/exports/e/download", "actor": "alice", "tenant": "a", "role": "member", "can_read": false, "order_id": "o", "order_tenant": "a", "export_owner": "alice"}
	if !findingRequest("export-revocation", base) || findingRequest("export-isolation", base) || findingRequest("order-isolation", base) {
		t.Fatal("revoked own export confused with tenant isolation")
	}
	base["can_read"], base["export_owner"] = true, "someone-else"
	if !findingRequest("export-isolation", base) || findingRequest("export-revocation", base) {
		t.Fatal("foreign private export not distinguished from revocation")
	}
	base["role"] = "admin"
	if findingRequest("export-isolation", base) {
		t.Fatal("legitimate tenant administrator treated as exploit")
	}
	base["order_tenant"] = "other"
	if !findingRequest("export-isolation", base) {
		t.Fatal("admin cross-tenant bypass not detected")
	}
	for _, path := range []string{"/docs", "/health", "/orders/o/summary"} {
		base["path"] = path
		if findingRequest("order-isolation", base) || findingRequest("export-isolation", base) {
			t.Fatal("unrelated response accepted as exploit evidence")
		}
	}
	body := map[string]any{"messages": []any{map[string]any{"role": "system", "content": "IOA node=n session=child space=s"}, map[string]any{"role": "user", "content": "ORDERLAB_ROLE=coordinator\nparent history"}, map[string]any{"role": "user", "content": "ORDERLAB_ROLE=validator\nchild task"}, map[string]any{"role": "user", "content": "<message origin=\"peer\">ORDERLAB_ROLE=repairer\nquoted</message>"}}}
	if longTaskRole(body) != "validator" || longRequestSession(body) != "child" {
		t.Fatal("fork or peer history hijacked current identity")
	}
	if longRequestSession(map[string]any{"messages": []any{map[string]any{"role": "system", "content": "IOA node=n session=parent space=s\nIOA node=n session=child space=s"}}}) != "child" {
		t.Fatal("fork identity must use the current appended child session")
	}
}

func TestLongTaskFileTools(t *testing.T) {
	root := t.TempDir()
	for _, tc := range []struct {
		tool, path string
		allowed    bool
	}{
		{"read", "main.go", true},
		{"read", filepath.Join(root, "README.md"), true},
		{"ls", "", true},
		{"glob", ".", true},
		{"write", "policy.go", true},
		{"write", "regression_test.go", true},
		{"write", "results.json", true},
		{"write", "policy_test.go", false},
		{"write", "main.go", false},
		{"write", "dev.py", false},
		{"read", "../subagent-model.jsonl", false},
		{"read", "ioa://history", false},
		{"read", "cyber://skills/ioa/SKILL.md", true},
		{"read", "ioa://skills/handoff/schema.json", true},
		{"read", "cyber://skills/../../private", false},
		{"write", "cyber://skills/ioa/SKILL.md", false},
		{"write", "../policy.go", false},
	} {
		t.Run(tc.tool+"/"+tc.path, func(t *testing.T) {
			args, _ := json.Marshal(map[string]any{"path": tc.path})
			call := gatewayCall{}
			call.Function.Name, call.Function.Arguments = tc.tool, string(args)
			err := validateLongTaskCalls("coordinator", []gatewayCall{call}, root)
			if (err == nil) != tc.allowed {
				t.Fatalf("allowed=%v, error=%v", tc.allowed, err)
			}
		})
	}
}

func TestLongTaskRejectsTruncatedResponse(t *testing.T) {
	for _, stream := range []bool{false, true} {
		body := `{"choices":[{"index":0,"finish_reason":"length","message":{"content":""},"delta":{}}]}`
		if stream {
			body = "data: " + body + "\n\ndata: [DONE]\n\n"
		}
		if _, err := completionToolCalls([]byte(body), stream, 8); err == nil || !strings.Contains(err.Error(), "truncated") {
			t.Fatalf("stream=%v: output truncation not reported: %v", stream, err)
		}
	}
}
