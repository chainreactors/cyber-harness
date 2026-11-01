package inbox_test

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"reflect"
	"regexp"
	"strconv"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	mailbox "github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
	loopext "github.com/chainreactors/cyber/exts/agent"
	inboxext "github.com/chainreactors/cyber/exts/inbox"
	ioaclient "github.com/chainreactors/cyber/exts/ioa/client"
	promptext "github.com/chainreactors/cyber/exts/prompt"
	sessionext "github.com/chainreactors/cyber/exts/session"
	subagentext "github.com/chainreactors/cyber/exts/subagent"
	cfg "github.com/chainreactors/cyber/pkg/config"
	"github.com/chainreactors/cyber/pkg/testutil/apptest"
	"github.com/chainreactors/cyber/pkg/testutil/hosttest"
	peerclient "github.com/chainreactors/ioa/client"
	"github.com/chainreactors/ioa/protocols"
	ioaserver "github.com/chainreactors/ioa/server"
)

func TestReconciliationScenario(t *testing.T) {
	reconciliationScenario(t, nil)
}

func TestLiveReconciliationScenario(t *testing.T) {
	reconciliationScenario(t, liveRoutingModel(t))
}

// The same business oracle checks both variants. The offline variant delegates
// actual CSV calculations to Python through the worker's real bash tool. Live
// workers receive only inputs/contracts and must implement their own solution.
func reconciliationScenario(t *testing.T, live provider.Provider) {
	t.Helper()
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	defer cancel()
	dir := os.Getenv("CYBER_INBOX_SCENARIO_OUTPUT")
	if dir == "" {
		dir = t.TempDir()
	} else {
		var err error
		dir, err = filepath.Abs(dir)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.MkdirAll(dir, 0700); err != nil {
			t.Fatal(err)
		}
		entries, err := os.ReadDir(dir)
		if err != nil || len(entries) != 0 {
			t.Fatalf("scenario output must be empty: %s (%v)", dir, err)
		}
	}
	data := make(map[string][]byte)
	for _, name := range []string{"orders.csv", "events.csv", "late.csv"} {
		b, err := os.ReadFile(filepath.Join("testdata", "reconciliation", name))
		if err != nil {
			t.Fatal(err)
		}
		data[name] = b
		if name != "late.csv" {
			if err := os.WriteFile(filepath.Join(dir, name), b, 0600); err != nil {
				t.Fatal(err)
			}
		}
	}
	var python string
	if live == nil {
		var err error
		python, err = exec.LookPath("python")
		if err != nil {
			t.Skip("offline reconciliation requires Python 3")
		}
		program, err := os.ReadFile(filepath.Join("testdata", "reconciliation", "reference.py"))
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(filepath.Join(dir, "reference.py"), program, 0600); err != nil {
			t.Fatal(err)
		}
	}
	roles := regexp.MustCompile(`\bROLE=(ledger|quality|reporter)\b`)
	versions := regexp.MustCompile(`\bVERSION=([12])\b`)
	var commands atomic.Int32
	var restored atomic.Bool
	p := modelFunc(func(callCtx context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		isRouter := len(r.Tools) == 1 && r.Tools[0].Name == "inbox_route"
		if isRouter && live == nil {
			if r.Messages[len(r.Messages)-1].Role == "tool" {
				return response("admitted"), nil
			}
			text := lastInput(r)
			parts := strings.SplitN(strings.TrimPrefix(text, "Current routing context:\n"), "\nOriginal message:\n", 2)
			if len(parts) != 2 {
				return nil, fmt.Errorf("missing original routing message")
			}
			contextText := parts[0]
			var routing struct {
				Meta  map[string]any `json:"meta"`
				Tasks []struct {
					ID    string `json:"task_id"`
					Label string `json:"label"`
				} `json:"tasks"`
			}
			if err := json.Unmarshal([]byte(contextText), &routing); err != nil {
				return nil, err
			}
			if routing.Meta["kind"] == "subagent_feedback" {
				var source, reporter string
				for _, task := range routing.Tasks {
					if task.ID == routing.Meta["task_id"] {
						source = task.Label
					}
					if task.Label == "reporter" {
						reporter = task.ID
					}
				}
				if source == "reporter" {
					return toolResponse("inbox_route", map[string]any{"action": "reply"}), nil
				}
				if reporter == "" {
					return nil, fmt.Errorf("missing report task")
				}
				return toolResponse("inbox_route", map[string]any{"task_id": reporter}), nil
			}
			if task, _ := routing.Meta["task_id"].(string); task != "" {
				return toolResponse("inbox_route", map[string]any{"task_id": task}), nil
			}
			role := roles.FindStringSubmatch(parts[1])
			if len(role) != 2 {
				return nil, fmt.Errorf("missing routing role")
			}
			return toolResponse("inbox_route", map[string]any{"new_task": map[string]any{"label": role[1]}}), nil
		}
		role, version, lastUser := "", 1, -1
		for i, message := range r.Messages {
			if message.Role != "user" {
				continue
			}
			lastUser = i
			text := provider.MessageText(message)
			if match := roles.FindStringSubmatch(text); role == "" && len(match) == 2 {
				role = match[1]
			}
			for _, match := range versions.FindAllStringSubmatch(text, -1) {
				v, _ := strconv.Atoi(match[1])
				version = max(version, v)
			}
		}
		if !isRouter && role == "reporter" && version == 2 {
			for _, message := range r.Messages {
				if strings.Contains(provider.MessageText(message), "ROLE=reporter VERSION=1") {
					restored.Store(true)
				}
			}
		}
		if live != nil {
			return live.ChatCompletion(callCtx, r)
		}
		if role == "" {
			return nil, fmt.Errorf("worker lost role")
		}
		for _, message := range r.Messages[lastUser+1:] {
			for _, part := range message.Content {
				if result := part.GetToolResult(); result != nil && result.Name == "bash" {
					if result.IsError {
						return nil, fmt.Errorf("reference worker failed: %s", coretool.ResultText(result))
					}
					return response(coretool.ResultText(result)), nil
				}
			}
		}
		quote := func(s string) string { return "'" + strings.ReplaceAll(filepath.ToSlash(s), "'", "'\"'\"'") + "'" }
		command := fmt.Sprintf("%s %s %s %s %d", quote(python), quote(filepath.Join(dir, "reference.py")), quote(dir), role, version)
		commands.Add(1)
		return toolResponse("bash", map[string]any{"command": command}), nil
	})
	store := ioaserver.NewMemoryStore()
	defer store.Close()
	server := httptest.NewServer(ioaserver.NewHTTPHandler(ioaserver.NewService(store, "reconcile")))
	defer server.Close()
	endpoint := strings.Replace(server.URL, "http://", "http://reconcile@", 1)
	monitor, err := peerclient.NewClient(endpoint, "")
	if err != nil {
		t.Fatal(err)
	}
	if err := monitor.EnsureRegistered(ctx, "settlement-monitor", "", nil); err != nil {
		t.Fatal(err)
	}
	space, err := monitor.Space(ctx, "reconciliation", "late settlement events")
	if err != nil {
		t.Fatal(err)
	}
	feedback := make(chan mailbox.Message, 32)
	state := filepath.Join(dir, "state")
	receiver := ""
	open := func() (*inboxext.Router, *extension.Set) {
		t.Helper()
		f := apptest.NewFixture(t, nil, nil)
		f.Providers.Set(p, provider.ProviderConfig{Model: "scenario", ContextWindow: 200000})
		ioaConfig, err := ioaclient.ConfigFromOption(&cfg.Option{Command: cfg.RunModeInbox, InboxOptions: cfg.InboxOptions{StateDir: state}, Extensions: cfg.Values{ioaclient.ConfigKey: {"url": endpoint, "space": "reconciliation"}}})
		if err != nil {
			t.Fatal(err)
		}
		connection := ioaclient.New(*ioaConfig)
		router := inboxext.New(inboxext.Config{StateDir: state, MaxActive: 2, Instructions: `User requests identify ROLE=reporter, ROLE=ledger or ROLE=quality. Create one new_task for each distinct initial role, with label equal to that role; omit name. Honor explicit task_id on follow-ups. Route subagent_feedback from ledger/quality to the EXISTING reporter task, including waiting or failure evidence. For reporter feedback use action=reply. Never create a task from feedback; never route feedback back to its source. Do not execute work or answer on behalf of workers.`, Notify: func(_ context.Context, m mailbox.Message) error {
			select {
			case feedback <- m:
				return nil
			case <-ctx.Done():
				return ctx.Err()
			}
		}})
		adapter := ioaclient.NewCollaboration(ioaclient.CollaborationOptions{Receive: router.Router().Deliver, LoadCursor: router.Router().LoadCursor, SaveCursor: router.Router().SaveCursor})
		entries := append(apptest.Entries(t, f), promptext.New(), loopext.New(agent.StandardLoop{}), subagentext.New(), connection, sessionext.New(session.Config{}), subagentext.NewTools(), adapter, router)
		set := hosttest.Load(t, ctx, entries...)
		if bound, err := connection.Service().ReadySpace(ctx); err != nil || bound != space.ID {
			t.Fatalf("IOA space=%s error=%v", bound, err)
		}
		id := connection.Service().Client().NodeID()
		if receiver != "" && receiver != id {
			t.Fatal("receiver identity changed across restart")
		}
		receiver = id
		return router.Router(), set
	}
	router, set := open()
	defer func() { closeActive(t, set) }()
	contract := fmt.Sprintf(`Work only in %q. Use bash to read CSVs and implement your own local solution; do not create subagents or access external services. Amounts are integer minor units, separately by currency. Expected = total_minor - approved_refund_minor. Deduplicate event_id; conflicting duplicates are errors. capture adds money, refund/void subtract. Validate void references to a same-order/currency/amount capture and reject repeated voids. Unknown orders and currency mismatches are quarantined and excluded from valid balances. Row order is not transaction order.
For version one use orders.csv and events.csv. On a future version update also use late.csv and recompute. fingerprint is lowercase SHA-256 of the exact raw bytes of the applicable CSV files joined by one newline, in that order.
All JSON artifacts include integer version and string fingerprint. ledger-vN.json contains orders, discrepancies (nonzero difference only) and totals keyed by currency; every order has order_id, currency, expected_minor, actual_minor, difference_minor. quality-vN.json contains source_rows, unique_events, duplicate_rows, orphan_events (sorted event IDs), currency_mismatches (sorted event IDs), quarantined_minor (by currency), void_events, errors (list).
reporter reads both evidence files and rechecks arithmetic, matching version/fingerprint and errors. Missing or stale evidence means finish with EVIDENCE role=reporter VERSION=N status=waiting, with no final report for that version. Do not wait or poll; incoming feedback will resume you. Once complete, write report-vN.json with status=ready, totals, discrepancies, quality (the complete quality document), version/fingerprint, plus a human readable report-vN.md listing unresolved order IDs and amounts.
Ledger/quality finish with EVIDENCE role=YOUR_ROLE VERSION=N status=ready after writing their artifact. Reporter finishes with its matching EVIDENCE status. Follow-up evidence versions select the newest version. Do not claim ready without actual artifacts.`, filepath.ToSlash(dir))
	tasks := make(map[string]string)
	var reporterMessage string
	for _, role := range []string{"reporter", "ledger", "quality"} {
		m := mailbox.NewUserMessage(fmt.Sprintf("ROLE=%s VERSION=1\n%s", role, contract))
		m.Message.Id = "reconciliation-" + role
		if role == "reporter" {
			reporterMessage = m.Message.Id
		}
		tasks[role] = activeSubmit(t, ctx, router, m)
	}
	waitReport := func(version int, status string) mailbox.Message {
		t.Helper()
		for {
			m := waitFor(t, ctx, feedback)
			if m.Meta["status"] != "completed" {
				t.Fatalf("report task failed: %v %s", m.Meta, provider.MessageText(m.Message))
			}
			text := provider.MessageText(m.Message)
			if strings.Contains(text, fmt.Sprintf("VERSION=%d", version)) && strings.Contains(text, "status="+status) {
				if m.Meta["task_id"] != tasks["reporter"] || m.Meta["reply_message"] != reporterMessage {
					t.Fatalf("report feedback lost requester: %v; want task=%s message=%s", m.Meta, tasks["reporter"], reporterMessage)
				}
				return m
			}
		}
	}
	waitReport(1, "ready")
	db, err := sql.Open("sqlite", filepath.Join(state, "active.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	idle := func() {
		t.Helper()
		for {
			var n int
			err := db.QueryRow(`SELECT (SELECT count(*) FROM messages WHERE state IN ('pending','routing')) + (SELECT count(*) FROM inputs WHERE state IN ('pending','running')) + (SELECT count(*) FROM tasks WHERE state IN ('queued','running')) + (SELECT count(*) FROM outbox WHERE state='pending')`).Scan(&n)
			if err != nil {
				t.Fatal(err)
			}
			if n == 0 {
				return
			}
			select {
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
	idle()
	closeActive(t, set)
	if err := os.WriteFile(filepath.Join(dir, "late.csv"), data["late.csv"], 0600); err != nil {
		t.Fatal(err)
	}
	// A plausible old artifact under the new filename must not satisfy fan-in.
	stale, err := os.ReadFile(filepath.Join(dir, "quality-v1.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(dir, "quality-v2.json"), stale, 0600); err != nil {
		t.Fatal(err)
	}
	send := func(role string) protocols.Message {
		t.Helper()
		text := fmt.Sprintf("ROLE=%s VERSION=2\nLate settlement events are now available in late.csv. Correction/clarification: compare after approved refunds, not gross order totals. Recompute your evidence from all input files and keep currency separation. %s", role, contract)
		m, err := monitor.Send(ctx, space.ID, protocols.SendMessage{Content: map[string]any{"text": text}, Refs: &protocols.Ref{Nodes: []string{receiver}}, Meta: map[string]any{"source_session_id": "settlement-monitor", "task_id": tasks[role]}})
		if err != nil {
			t.Fatal(err)
		}
		return m
	}
	offline := send("ledger")
	router, set = open()
	waitReport(2, "waiting")
	if _, err := os.Stat(filepath.Join(dir, "report-v2.json")); !os.IsNotExist(err) {
		t.Fatalf("stale evidence produced final report: %v", err)
	}
	idle()
	before := commands.Load()
	duplicate := mailbox.NewMessage(mailbox.OriginPeer, "user", "same admitted source envelope")
	duplicate.Message.Id = offline.ID
	duplicate.Meta = map[string]any{"sender": monitor.NodeID(), "message_id": offline.ID, "source_session_id": "settlement-monitor", "task_id": tasks["ledger"]}
	if got := activeSubmit(t, ctx, router, duplicate); got != tasks["ledger"] {
		t.Fatalf("duplicate changed task: %s", got)
	}
	idle()
	if commands.Load() != before {
		t.Fatal("duplicate replay reexecuted a worker")
	}
	send("quality")
	waitReport(2, "ready")
	idle()
	var result struct {
		Version     int    `json:"version"`
		Fingerprint string `json:"fingerprint"`
		Status      string `json:"status"`
		Totals      map[string]struct {
			Expected   int `json:"expected_minor"`
			Actual     int `json:"actual_minor"`
			Difference int `json:"difference_minor"`
		} `json:"totals"`
		Discrepancies []struct {
			ID         string `json:"order_id"`
			Currency   string `json:"currency"`
			Difference int    `json:"difference_minor"`
		} `json:"discrepancies"`
		Quality struct {
			Version     int            `json:"version"`
			Fingerprint string         `json:"fingerprint"`
			Rows        int            `json:"source_rows"`
			Unique      int            `json:"unique_events"`
			Duplicates  int            `json:"duplicate_rows"`
			Orphans     []string       `json:"orphan_events"`
			Mismatches  []string       `json:"currency_mismatches"`
			Quarantined map[string]int `json:"quarantined_minor"`
			Voids       int            `json:"void_events"`
			Errors      []string       `json:"errors"`
		} `json:"quality"`
	}
	b, err := os.ReadFile(filepath.Join(dir, "report-v2.json"))
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal(b, &result); err != nil {
		t.Fatal(err)
	}
	sum := sha256.Sum256(bytes.Join([][]byte{data["orders.csv"], data["events.csv"], data["late.csv"]}, []byte{'\n'}))
	fingerprint := hex.EncodeToString(sum[:])
	if result.Version != 2 || result.Fingerprint != fingerprint || result.Status != "ready" || result.Quality.Fingerprint != fingerprint || result.Quality.Version != 2 {
		t.Fatalf("stale report: %s", b)
	}
	if len(result.Totals) != 2 {
		t.Fatalf("currencies were mixed: %v", result.Totals)
	}
	for currency, want := range map[string][3]int{"CNY": {63000, 56000, -7000}, "USD": {7200, 7300, 100}} {
		got := result.Totals[currency]
		if [3]int{got.Expected, got.Actual, got.Difference} != want {
			t.Fatalf("%s totals=%+v want=%v", currency, got, want)
		}
	}
	differences := make(map[string]int)
	for _, row := range result.Discrepancies {
		differences[row.ID] = row.Difference
	}
	if len(result.Discrepancies) != 3 || !reflect.DeepEqual(differences, map[string]int{"A008": -1000, "A009": 100, "A010": -6000}) {
		t.Fatalf("unresolved orders=%v", result.Discrepancies)
	}
	q := result.Quality
	if q.Rows != 25 || q.Unique != 21 || q.Duplicates != 4 || q.Voids != 1 || len(q.Errors) != 0 || !reflect.DeepEqual(q.Orphans, []string{"e16"}) || !reflect.DeepEqual(q.Mismatches, []string{"e15"}) || !reflect.DeepEqual(q.Quarantined, map[string]int{"CNY": 700, "USD": 5900}) {
		t.Fatalf("quality=%+v", q)
	}
	for _, name := range []string{"ledger-v2.json", "quality-v2.json"} {
		b, err := os.ReadFile(filepath.Join(dir, name))
		if err != nil {
			t.Fatal(err)
		}
		var evidence struct {
			Version     int    `json:"version"`
			Fingerprint string `json:"fingerprint"`
			Orders      []struct {
				ID       string `json:"order_id"`
				Expected int    `json:"expected_minor"`
				Actual   int    `json:"actual_minor"`
			} `json:"orders"`
		}
		if err := json.Unmarshal(b, &evidence); err != nil {
			t.Fatal(err)
		}
		if evidence.Version != 2 || evidence.Fingerprint != fingerprint {
			t.Fatalf("stale primary evidence %s: %s", name, b)
		}
		if name == "ledger-v2.json" {
			want := map[string][2]int{"A001": {10000, 10000}, "A002": {20000, 20000}, "A003": {13000, 13000}, "A004": {0, 0}, "A005": {5000, 5000}, "A006": {1200, 1200}, "A007": {2500, 2500}, "A008": {9000, 8000}, "A009": {3500, 3600}, "A010": {6000, 0}, "A011": {0, 0}, "A012": {0, 0}}
			if len(evidence.Orders) != len(want) {
				t.Fatalf("ledger missing orders: %s", b)
			}
			for _, row := range evidence.Orders {
				pair, ok := want[row.ID]
				if !ok || pair != [2]int{row.Expected, row.Actual} {
					t.Fatalf("wrong ledger order: %+v", row)
				}
				delete(want, row.ID)
			}
			if len(want) != 0 {
				t.Fatalf("ledger repeated/missing orders: %v", want)
			}
		}
	}
	markdown, err := os.ReadFile(filepath.Join(dir, "report-v2.md"))
	if err != nil {
		t.Fatal(err)
	}
	for _, expected := range []string{"A008", "A009", "A010", "CNY", "USD", fingerprint} {
		if !strings.Contains(string(markdown), expected) {
			t.Fatalf("report markdown missing %q", expected)
		}
	}
	var taskCount, labels int
	if err := db.QueryRow("SELECT count(*),count(DISTINCT label) FROM tasks").Scan(&taskCount, &labels); err != nil {
		t.Fatal(err)
	}
	if taskCount != 3 || labels != 3 || !restored.Load() {
		t.Fatalf("task/history recovery: tasks=%d labels=%d restored=%v", taskCount, labels, restored.Load())
	}
	t.Logf("reconciliation solved: CNY 63000/56000/-7000, USD 7200/7300/+100; 3 unresolved orders; 25 rows/21 events/4 duplicates; 3 stable tasks; offline IOA replay, stale-evidence gate and duplicate suppression passed; artifacts=%s", dir)
}
