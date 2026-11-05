package inbox_test

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"net/http/httptest"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/cyber/agent"
	agenthooks "github.com/chainreactors/cyber/agent/hooks"
	mailbox "github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	harness "github.com/chainreactors/cyber/core/collaboration"
	"github.com/chainreactors/cyber/core/extension"
	coretool "github.com/chainreactors/cyber/core/tool"
	loopext "github.com/chainreactors/cyber/exts/agent"
	inboxext "github.com/chainreactors/cyber/exts/inbox"
	ioaclient "github.com/chainreactors/cyber/exts/ioa/client"
	nativeext "github.com/chainreactors/cyber/exts/native"
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

// This fixture owns only test evidence and bounded model instrumentation. It
// never chooses tools, returns scripted answers, or supplies computed solutions.
type complexLive struct {
	t       *testing.T
	ctx     context.Context
	cancel  context.CancelFunc
	dir     string
	model   provider.Provider
	calls   atomic.Int32
	workers atomic.Int32
	peak    atomic.Int32
	full    atomic.Int32
	mu      sync.Mutex
	trace   *os.File
}

func newComplexLive(t *testing.T, name string) *complexLive {
	t.Helper()
	live := liveRoutingModel(t)
	ctx, cancel := context.WithTimeout(t.Context(), 8*time.Minute)
	t.Cleanup(cancel)
	parent := os.Getenv("CYBER_INBOX_COMPLEX_OUTPUT")
	if parent == "" {
		parent = t.TempDir()
	}
	if err := os.MkdirAll(parent, 0700); err != nil {
		t.Fatal(err)
	}
	dir, err := os.MkdirTemp(parent, name+"-")
	if err != nil {
		t.Fatal(err)
	}
	dir, err = filepath.Abs(dir)
	if err != nil {
		t.Fatal(err)
	}
	f, err := os.Create(filepath.Join(dir, "model-trace.jsonl"))
	if err != nil {
		t.Fatal(err)
	}
	c := &complexLive{t: t, ctx: ctx, cancel: cancel, dir: dir, trace: f}
	t.Cleanup(func() { _ = f.Close() })
	c.model = modelFunc(func(ctx context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		id := c.calls.Add(1)
		router := len(r.Tools) == 1 && r.Tools[0].Name == "inbox_route"
		if !router {
			n := c.workers.Add(1)
			for old := c.peak.Load(); n > old && !c.peak.CompareAndSwap(old, n); old = c.peak.Load() {
			}
			defer c.workers.Add(-1)
		}
		var tools []any
		if len(r.Messages) > 0 {
			for _, part := range r.Messages[len(r.Messages)-1].Content {
				if result := part.GetToolResult(); result != nil {
					tools = append(tools, map[string]any{"name": result.Name, "error": result.IsError, "text": coretool.ResultText(result)})
				}
			}
		}
		c.record(map[string]any{"event": "request", "call": id, "session": r.SessionID, "router": router, "messages": len(r.Messages), "tool_results": tools})
		res, err := live.ChatCompletion(ctx, r)
		c.record(map[string]any{"event": "response", "call": id, "session": r.SessionID, "router": router, "response": res, "error": fmt.Sprint(err)})
		return res, err
	})
	t.Logf("complex live evidence: %s", dir)
	return c
}

func (c *complexLive) record(v any) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if err := json.NewEncoder(c.trace).Encode(v); err != nil {
		c.t.Errorf("trace: %v", err)
	}
}

func (c *complexLive) write(name string, v any) {
	c.t.Helper()
	b, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		c.t.Fatal(err)
	}
	if err = os.WriteFile(filepath.Join(c.dir, name), append(b, '\n'), 0600); err != nil {
		c.t.Fatal(err)
	}
}

func (c *complexLive) read(name string, v any) []byte {
	c.t.Helper()
	b, err := os.ReadFile(filepath.Join(c.dir, name))
	if err != nil {
		c.t.Fatal(err)
	}
	if err = json.Unmarshal(b, v); err != nil {
		c.t.Fatalf("%s: %v: %s", name, err, b)
	}
	return b
}

func (c *complexLive) settled(state string, tasks int) {
	c.t.Helper()
	db, err := sql.Open("sqlite", filepath.Join(state, "active.sqlite"))
	if err != nil {
		c.t.Fatal(err)
	}
	defer db.Close()
	for {
		var pending, n int
		err = db.QueryRowContext(c.ctx, `SELECT (SELECT count(*) FROM messages WHERE state<>'done')+(SELECT count(*) FROM routes WHERE state<>'done')+(SELECT count(*) FROM inputs WHERE state<>'done')+(SELECT count(*) FROM tasks WHERE state<>'completed')+(SELECT count(*) FROM outbox WHERE state<>'sent')`).Scan(&pending)
		if err != nil {
			c.t.Fatal(err)
		}
		if pending == 0 {
			if err = db.QueryRow("SELECT count(*) FROM tasks").Scan(&n); err != nil {
				c.t.Fatal(err)
			}
			if n != tasks {
				c.t.Fatalf("tasks=%d want=%d", n, tasks)
			}
			rows, err := db.Query("SELECT task_id,count(*) FROM attempts GROUP BY task_id")
			if err != nil {
				c.t.Fatal(err)
			}
			attempts := map[string]int{}
			for rows.Next() {
				var id string
				var count int
				if err = rows.Scan(&id, &count); err != nil {
					c.t.Fatal(err)
				}
				attempts[id] = count
			}
			if err = errors.Join(rows.Err(), rows.Close()); err != nil {
				c.t.Fatal(err)
			}
			c.record(map[string]any{"event": "settled", "state": state, "tasks": n, "attempts": attempts, "model_calls": c.calls.Load(), "peak_model_workers": c.peak.Load()})
			return
		}
		select {
		case <-time.After(25 * time.Millisecond):
		case <-c.ctx.Done():
			c.t.Fatalf("unsettled state %s pending=%d: %v", state, pending, c.ctx.Err())
		}
	}
}

const complexRouting = "Create a separate anonymous new_task for each initial ROLE, label it with that ROLE. Honor explicit task_id for every correction or continuation. For owned subagent_feedback use action=record, which must deliver it to the requester. For incoming peer task_result route to its explicit task_id. Never create tasks from feedback, execute work yourself, or route results back to their source."

func TestLiveComplexVersionedReconciliation(t *testing.T) {
	c := newComplexLive(t, "versioned-reconciliation")
	work := filepath.Join(c.dir, "work")
	t.Setenv("CYBER_INBOX_SCENARIO_OUTPUT", work)
	reconciliationScenario(t, c.model)
	c.settled(filepath.Join(work, "state"), 3)
}

func complexCompleted(t *testing.T, ctx context.Context, ch <-chan mailbox.Message, task string) mailbox.Message {
	t.Helper()
	m := waitFor(t, ctx, ch)
	if m.Meta.Status != "completed" || task != "" && m.Meta.TaskID != task {
		t.Fatalf("unexpected completion: %v %s", m.Meta, provider.MessageText(m.Message))
	}
	return m
}

// A real DAG computation, not a wait-token test: each consumer independently
// derives a different artifact from normalized evidence under one root slot.
func TestLiveComplexDAGCapacityAndTmux(t *testing.T) {
	c := newComplexLive(t, "dag")
	type job struct {
		ID       string   `json:"id"`
		Duration int      `json:"duration"`
		Rate     int      `json:"rate"`
		Team     string   `json:"team"`
		Deadline int      `json:"deadline"`
		Deps     []string `json:"deps"`
	}
	jobs := []job{}
	finish := map[string]int{}
	paths := map[string][]string{}
	costs := map[string]int{}
	late := map[string]int{}
	for i := 0; i < 24; i++ {
		j := job{ID: fmt.Sprintf("J%02d", i), Duration: 2 + i*7%9, Rate: 31 + i*13%37, Team: []string{"infra", "app", "audit"}[i%3], Deadline: 4 + i, Deps: []string{}}
		if i >= 3 {
			j.Deps = append(j.Deps, fmt.Sprintf("J%02d", i-3))
		}
		if i >= 5 && i%4 == 0 {
			j.Deps = append(j.Deps, fmt.Sprintf("J%02d", i-5))
		}
		slices.Sort(j.Deps)
		start := 0
		var prev string
		for _, d := range j.Deps {
			if finish[d] > start || finish[d] == start && (prev == "" || d < prev) {
				start = finish[d]
				prev = d
			}
		}
		finish[j.ID] = start + j.Duration
		paths[j.ID] = append(append([]string{}, paths[prev]...), j.ID)
		costs[j.Team] += j.Duration * j.Rate
		if finish[j.ID] > j.Deadline {
			late[j.ID] = finish[j.ID] - j.Deadline
		}
		jobs = append(jobs, j)
	}
	end := ""
	for _, j := range jobs {
		if end == "" || finish[j.ID] > finish[end] || finish[j.ID] == finish[end] && j.ID < end {
			end = j.ID
		}
	}
	raw := append([]job{}, jobs...)
	slices.Reverse(raw)
	raw = append(raw, raw[3], raw[11])
	c.write("raw-jobs.json", raw)
	results := make(chan mailbox.Message, 16)
	waiting := make(chan string, 8)
	var woken atomic.Bool
	unexpectedWait := make(chan string, 3)
	state := filepath.Join(c.dir, "state")
	f, router, _, set := activeHost(t, c.model, inboxext.Config{StateDir: state, MaxActive: 1, Instructions: complexRouting, Notify: func(_ context.Context, m mailbox.Message) error { results <- m; return nil }}, nativeext.New())
	defer closeActive(t, set)
	sub := agenthooks.InboxWait.On(f.Hooks, "complex.actual-suspend", func(_ context.Context, e agenthooks.InboxWaitEvent) (struct{}, error) {
		c.record(map[string]any{"event": "wait", "session": e.SessionID, "waiting": e.Waiting})
		if e.Waiting {
			if woken.Load() {
				unexpectedWait <- e.SessionID
				return struct{}{}, nil
			}
			waiting <- e.SessionID
		}
		return struct{}{}, nil
	})
	defer sub.Close(context.Background())
	contract := fmt.Sprintf(`Work only in %q, with bash and your own Python implementation; no subagents, external services, polling or sleep. Files raw-jobs.json and graph.json are read-only for consumers. Jobs form a DAG with duration, hourly rate, team, deadline, deps. Earliest start=max(dependency finish) or zero; finish=start+duration. For tied predecessors choose lexicographically smallest ID. For critical final job choose maximum finish then lexicographically smallest ID. No resource conflicts besides dependencies. All quantities are integers. This is a ONE-SHOT task with two phases: (1) Call inbox_wait alone exactly once as your first action, timeout=0, then wait for the graph-ready message. (2) After that message read graph.json, compute and verify only your assigned artifact, then return a final assistant response and END. Never call inbox_wait again after the first wake; there is no later work or second message to await. Never generate graph.json or another role's result. You may use stdin Python or role-prefixed scripts inside this directory.`, filepath.ToSlash(c.dir))
	roles := map[string]string{"schedule": "Write schedule.json with finishes (map job ID to earliest finish), makespan, critical_path (ordered IDs).", "cost": "Write cost.json with by_team (duration*rate sums), total (sum all costs).", "deadline": "Write deadline.json with late (map late job ID to positive lateness), total_lateness."}
	tasks := map[string]string{}
	executions := map[string]string{}
	for _, role := range []string{"schedule", "cost", "deadline"} {
		tasks[role] = activeSubmit(t, c.ctx, router, mailbox.NewUserMessage("ROLE="+role+". "+contract+" "+roles[role]))
		select {
		case id := <-waiting:
			executions[tasks[role]] = id
		case m := <-results:
			t.Fatalf("consumer completed before suspension: %v %s", m.Meta, provider.MessageText(m.Message))
		case <-c.ctx.Done():
			t.Fatal(c.ctx.Err())
		}
	}
	producer := activeSubmit(t, c.ctx, router, mailbox.NewUserMessage(fmt.Sprintf(`ROLE=normalizer. In %q use bash/Python to read raw-jobs.json, deduplicate identical IDs (conflicting duplicates must fail), validate every dependency exists and graph is acyclic. Write graph.json as an array of unique jobs sorted by id with all original fields preserved. Do not compute or write consumer artifacts. Do not create subagents. Re-read and verify exactly 24 unique jobs before finishing.`, filepath.ToSlash(c.dir))))
	complexCompleted(t, c.ctx, results, producer)
	var graph []job
	c.read("graph.json", &graph)
	if !reflect.DeepEqual(graph, jobs) {
		t.Fatalf("normalizer corrupted DAG: %+v", graph)
	}
	woken.Store(true)
	for _, info := range f.Shell.Manager().List() {
		if info.Kind == "agent" && info.Name != "router" && info.State.Active() {
			args, _ := json.Marshal(map[string]any{"command": fmt.Sprintf("tmux send-keys -t %s 'Normalized graph is now ready; compute your assigned artifact from graph.json.' Enter", info.ID)})
			r, err := f.Shell.Execute(c.ctx, string(args))
			if err != nil || r.IsError {
				t.Fatalf("tmux wake: %v %v", r, err)
			}
		}
	}
	seen := map[string]bool{}
	for range 3 {
		var m mailbox.Message
		select {
		case m = <-results:
			if m.Meta.Status != "completed" {
				t.Fatalf("consumer failed: %v", m.Meta)
			}
		case id := <-unexpectedWait:
			t.Fatalf("one-shot consumer suspended again after wake: %s", id)
		case <-c.ctx.Done():
			t.Fatal(c.ctx.Err())
		}
		id := m.Meta.TaskID
		if seen[id] || executions[id] != m.Meta.ExecutionID {
			t.Fatalf("worker replaced or duplicated: %v", m.Meta)
		}
		seen[id] = true
	}
	var schedule struct {
		Finishes     map[string]int
		Makespan     int
		CriticalPath []string `json:"critical_path"`
	}
	c.read("schedule.json", &schedule)
	var cost struct {
		ByTeam map[string]int `json:"by_team"`
		Total  int
	}
	c.read("cost.json", &cost)
	var deadlines struct {
		Late  map[string]int
		Total int `json:"total_lateness"`
	}
	c.read("deadline.json", &deadlines)
	totalCost, totalLate := 0, 0
	for _, v := range costs {
		totalCost += v
	}
	for _, v := range late {
		totalLate += v
	}
	if !reflect.DeepEqual(schedule.Finishes, finish) || schedule.Makespan != finish[end] || !reflect.DeepEqual(schedule.CriticalPath, paths[end]) || !reflect.DeepEqual(cost.ByTeam, costs) || cost.Total != totalCost || !reflect.DeepEqual(deadlines.Late, late) || deadlines.Total != totalLate {
		t.Fatalf("business oracle mismatch: schedule=%+v cost=%+v deadlines=%+v", schedule, cost, deadlines)
	}
	c.settled(state, 4)
	if c.peak.Load() != 1 {
		t.Fatalf("single-slot limit violated: %d", c.peak.Load())
	}
	c.write("oracle.json", map[string]any{"finishes": finish, "critical_path": paths[end], "makespan": finish[end], "costs": costs, "late": late, "same_execution": executions, "peak": c.peak.Load()})
}

func TestLiveComplexUrgentAllocation(t *testing.T) {
	c := newComplexLive(t, "urgent-allocation")
	type item struct {
		ID     string `json:"id"`
		Weight int    `json:"weight"`
		Cost   int    `json:"cost"`
		Value  int    `json:"value"`
		Vendor string `json:"vendor"`
	}
	items := []item{}
	for i := 0; i < 16; i++ {
		items = append(items, item{fmt.Sprintf("P%02d", i), 2 + i*7%10, 5 + i*11%23, 9 + i*17%43, []string{"north", "south", "recall"}[i%3]})
	}
	c.write("products.json", items)
	bestValue, bestCost, bestWeight := -1, 0, 0
	var bestIDs []string
	draftValue, draftCost, draftWeight := -1, 0, 0
	var draftIDs []string
	for mask := 0; mask < 1<<len(items); mask++ {
		value, cost, weight := 0, 0, 0
		ids := []string{}
		ok := mask&(1<<3) != 0
		for i, p := range items {
			if mask&(1<<i) == 0 {
				continue
			}
			if p.Vendor == "recall" {
				ok = false
			}
			value += p.Value
			cost += p.Cost
			weight += p.Weight
			ids = append(ids, p.ID)
		}
		if cost <= 110 && weight <= 35 && (value > draftValue || value == draftValue && (cost < draftCost || cost == draftCost && (weight < draftWeight || weight == draftWeight && strings.Join(ids, ",") < strings.Join(draftIDs, ",")))) {
			draftValue, draftCost, draftWeight, draftIDs = value, cost, weight, ids
		}
		if !ok || cost > 75 || weight > 22 {
			continue
		}
		if value > bestValue || value == bestValue && (cost < bestCost || cost == bestCost && (weight < bestWeight || weight == bestWeight && strings.Join(ids, ",") < strings.Join(bestIDs, ","))) {
			bestValue, bestCost, bestWeight, bestIDs = value, cost, weight, ids
		}
	}
	blocked := make(chan string, 1)
	interrupted := make(chan string, 1)
	var gate atomic.Bool
	p := modelFunc(func(ctx context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		if !(len(r.Tools) == 1 && r.Tools[0].Name == "inbox_route") {
			for _, m := range r.Messages {
				for _, part := range m.Content {
					tr := part.GetToolResult()
					if tr == nil || tr.IsError || tr.Name != "bash" {
						continue
					}
					lines := strings.Split(strings.TrimSpace(coretool.ResultText(tr)), "\n")
					if lines[len(lines)-1] == "ALLOCATION_DRAFT_READY" && gate.CompareAndSwap(false, true) {
						blocked <- r.SessionID
						<-ctx.Done()
						interrupted <- r.SessionID
						return nil, ctx.Err()
					}
				}
			}
		}
		return c.model.ChatCompletion(ctx, r)
	})
	results := make(chan mailbox.Message, 12)
	state := filepath.Join(c.dir, "state")
	_, router, _, set := activeHost(t, p, inboxext.Config{StateDir: state, MaxActive: 2, Instructions: complexRouting, Notify: func(_ context.Context, m mailbox.Message) error { results <- m; return nil }})
	defer closeActive(t, set)
	task := activeSubmit(t, c.ctx, router, mailbox.NewUserMessage(fmt.Sprintf(`ROLE=allocation. Work in %q using bash/Python, no subagents. Read products.json. Find the optimal subset of distinct products maximizing value with total weight<=35 and cost<=110, no vendor exclusion or mandatory products. Tie-break lower cost, then lower weight, then lexicographically smallest sorted ID list. Write draft.json with ids,value,cost,weight and revision=1. After computing and committing the draft, read it back and print ALLOCATION_DRAFT_READY alone on the final output line of that bash command. Do not inspect model-trace.jsonl or state: they are harness internals, not business input. Do not write final.json yet. A subsequent scope correction will arrive; never recompute or overwrite the committed draft.`, filepath.ToSlash(c.dir))))
	first := waitFor(t, c.ctx, blocked)
	c.record(map[string]any{"event": "interrupt-gate", "session": first, "boundary": "after committed draft, before next model response"})
	draft, err := os.ReadFile(filepath.Join(c.dir, "draft.json"))
	if err != nil {
		t.Fatal(err)
	}
	type allocation struct {
		IDs                           []string
		Value, Cost, Weight, Revision int
	}
	var committed allocation
	if err = json.Unmarshal(draft, &committed); err != nil || committed.Revision != 1 || committed.Value != draftValue || committed.Cost != draftCost || committed.Weight != draftWeight || !reflect.DeepEqual(committed.IDs, draftIDs) {
		t.Fatalf("draft not committed exact optimum: %+v error=%v want=%v %d/%d/%d", committed, err, draftIDs, draftValue, draftCost, draftWeight)
	}
	audit := activeSubmit(t, c.ctx, router, mailbox.NewUserMessage(fmt.Sprintf(`ROLE=supplier-audit. In %q independently read products.json and write supplier-audit.json mapping each vendor name to {count, value_sum, cost_sum, weight_sum}. Use bash/Python, verify actual output. Touch only supplier-audit.json and your own uniquely named script. No subagents.`, filepath.ToSlash(c.dir))))
	complexCompleted(t, c.ctx, results, audit)
	update := mailbox.NewUserMessage("URGENT REVISION=2. Continue this allocation task. Recall excludes every product from vendor recall. P03 is mandatory. New total weight limit=22 and budget=75. Recompute the exact optimal subset by exhaustive search using the original tie-break rules. Write final.json containing ids (sorted), value,cost,weight,revision=2. Re-read to verify; preserve draft.json and supplier-audit.json. Finish only after this revision is complete.")
	update.Message.Id = "allocation-urgent-v2"
	update.Interrupt = true
	update.Priority = mailbox.PriorityHigh
	update.Meta = mailbox.Metadata{TaskID: task}
	if activeSubmit(t, c.ctx, router, update) != task {
		t.Fatal("urgent update forked task")
	}
	if waitFor(t, c.ctx, interrupted) != first {
		t.Fatal("wrong model request interrupted")
	}
	c.record(map[string]any{"event": "interrupt-observed", "session": first})
	m := complexCompleted(t, c.ctx, results, task)
	if m.Meta.ExecutionID != first {
		t.Fatalf("interrupt replaced original execution: %v", m.Meta)
	}
	var actual allocation
	c.read("final.json", &actual)
	if actual.Value != bestValue || actual.Cost != bestCost || actual.Weight != bestWeight || actual.Revision != 2 || !reflect.DeepEqual(actual.IDs, bestIDs) {
		t.Fatalf("allocation not exact optimum: %+v want=%v %d/%d/%d", actual, bestIDs, bestValue, bestCost, bestWeight)
	}
	type sums struct {
		Count  int `json:"count"`
		Value  int `json:"value_sum"`
		Cost   int `json:"cost_sum"`
		Weight int `json:"weight_sum"`
	}
	want := map[string]sums{}
	for _, p := range items {
		v := want[p.Vendor]
		v.Count++
		v.Value += p.Value
		v.Cost += p.Cost
		v.Weight += p.Weight
		want[p.Vendor] = v
	}
	var got map[string]sums
	c.read("supplier-audit.json", &got)
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("supplier audit mismatch: %v want %v", got, want)
	}
	after, err := os.ReadFile(filepath.Join(c.dir, "draft.json"))
	if err != nil || string(after) != string(draft) {
		t.Fatal("scope correction destroyed committed draft")
	}
	c.settled(state, 2)
	c.write("oracle.json", map[string]any{"ids": bestIDs, "value": bestValue, "cost": bestCost, "weight": bestWeight, "same_execution": first, "interrupted": true, "committed_draft": committed})
}

// Real IOA HTTP/SSE and the exact production collaboration adapter; the callback
// records accepted wire envelopes without influencing routing decisions.
func complexIOAHost(t *testing.T, c *complexLive, p provider.Provider, config inboxext.Config, endpoint, space string, received func(mailbox.Message)) (*inboxext.Router, *extension.Set, *ioaclient.Extension) {
	t.Helper()
	f := apptest.NewFixture(t, nil, nil)
	f.Providers.Set(p, provider.ProviderConfig{Model: os.Getenv("CYBER_MODEL"), ContextWindow: 200000, MaxTokens: 8192})
	options, err := ioaclient.ConfigFromOption(&cfg.Option{Command: cfg.RunModeInbox, InboxOptions: cfg.InboxOptions{StateDir: config.StateDir}, Extensions: cfg.Values{ioaclient.ConfigKey: {"url": endpoint, "space": space}}})
	if err != nil {
		t.Fatal(err)
	}
	connection := ioaclient.New(*options)
	var router *inboxext.Router
	// These two IOA business cases explicitly finish each batch/result. Waiting
	// instead must fail the case promptly, never be rescued by a synthetic wake.
	observeWait := func(_ context.Context, e agenthooks.InboxWaitEvent) (struct{}, error) {
		if e.Waiting {
			c.record(map[string]any{"event": "unexpected-wait", "session": e.SessionID})
			t.Errorf("completed-batch IOA worker entered unsolicited wait: %s", e.SessionID)
			c.cancel()
		}
		return struct{}{}, nil
	}
	adapter := ioaclient.NewCollaboration(ioaclient.CollaborationOptions{Receive: func(ctx context.Context, m mailbox.Message) error {
		err := router.Deliver(ctx, m)
		if errors.Is(err, mailbox.ErrInboxFull) {
			c.full.Add(1)
		}
		c.record(map[string]any{"event": "ioa-admission", "message_id": m.Message.Id, "feedback_id": m.Meta.FeedbackID, "error": fmt.Sprint(err)})
		if err == nil && received != nil {
			received(m)
		}
		return err
	}, LoadCursor: func(key string) (string, error) {
		if router == nil {
			return "", inboxext.ErrUnavailable
		}
		return router.LoadCursor(key)
	}, SaveCursor: func(key, value string) error {
		if router == nil {
			return inboxext.ErrUnavailable
		}
		return router.SaveCursor(key, value)
	}})
	config.Record = adapter.RecordInboxHandoff
	inbox := inboxext.New(config)
	router = inbox.Router()
	entries := append(apptest.Entries(t, f), promptext.New(), loopext.New(agent.StandardLoop{}), subagentext.New(), nativeext.New(), connection, sessionext.New(session.Config{}), subagentext.NewTools(), adapter, inbox)
	set := hosttest.Load(t, c.ctx, entries...)
	sub := agenthooks.InboxWait.On(f.Hooks, "complex.unexpected-ioa-wait", observeWait)
	t.Cleanup(func() { _ = sub.Close(context.Background()) })
	if _, err = connection.Service().ReadySpace(c.ctx); err != nil {
		t.Fatal(err)
	}
	return router, set, connection
}

func TestLiveComplexIOAInventoryReplay(t *testing.T) {
	c := newComplexLive(t, "ioa-inventory")
	type event struct {
		ID        string `json:"id"`
		Revision  int    `json:"revision"`
		SKU       string `json:"sku"`
		Warehouse string `json:"warehouse"`
		Kind      string `json:"kind"`
		Quantity  int    `json:"quantity"`
	}
	events := []event{}
	for i := 0; i < 48; i++ {
		events = append(events, event{fmt.Sprintf("E%02d", i), 1, []string{"A", "B", "C"}[i%3], []string{"east", "west"}[i%2], []string{"receive", "ship", "reserve", "release"}[i%4], 3 + i*7%17})
	}
	c.write("seed.json", events[:12])
	all := append([]event{}, events...)
	batches := make([][]event, 9)
	for i := 0; i < 6; i++ {
		batches[i] = append([]event{}, events[12+i*6:18+i*6]...)
	}
	batches[6] = []event{events[7], events[29], events[37]}
	correction := events[7]
	correction.Revision = 2
	correction.Quantity = 2
	newer := events[24]
	newer.Revision = 3
	newer.Quantity = 71
	batches[7] = []event{correction, newer}
	// The older revisions arrive AFTER both corrected events. They must not win
	// merely because their transport batch number is newer.
	batches[8] = []event{events[7], events[24], events[7]}
	all = append(all, correction, newer)
	for i, b := range batches {
		c.write(fmt.Sprintf("batch-%d.json", i+1), b)
	}
	store := ioaserver.NewMemoryStore()
	defer store.Close()
	server := httptest.NewServer(ioaserver.NewHTTPHandler(ioaserver.NewService(store, "inventory")))
	defer server.Close()
	endpoint := strings.Replace(server.URL, "http://", "http://inventory@", 1)
	peer, err := peerclient.NewClient(endpoint, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = peer.EnsureRegistered(c.ctx, "warehouse-events", "", nil); err != nil {
		t.Fatal(err)
	}
	space, err := peer.Space(c.ctx, "inventory", "inventory corrections")
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan mailbox.Message, 64)
	state := filepath.Join(c.dir, "state")
	config := inboxext.Config{StateDir: state, MaxActive: 1, Capacity: 2, Instructions: complexRouting, Notify: func(_ context.Context, m mailbox.Message) error { results <- m; return nil }}
	wires := make(chan mailbox.Message, 64)
	open := func() (*inboxext.Router, *extension.Set, *ioaclient.Extension) {
		return complexIOAHost(t, c, c.model, config, endpoint, "inventory", func(m mailbox.Message) { wires <- m })
	}
	router, set, connection := open()
	defer func() { closeActive(t, set) }()
	node := connection.Service().Client().NodeID()
	contract := fmt.Sprintf(`ROLE=inventory. Work only in %q with bash/Python, no subagents or external services. Maintain event-sourced warehouse inventory. Initially read seed.json. Followup BATCH=N PATH=file adds events from that file, even if N arrives out of order. Persist journal.json as {max_batch:integer,events:array} containing one highest-revision event per id (ignore older or identical duplicates); max_batch is maximum seen batch number, initially zero. For each sku@warehouse, receive increases on_hand, ship decreases on_hand, reserve increases reserved, release decreases reserved. available=on_hand-reserved. Negative balances are allowed and must be reported, not clamped. Recompute from the entire deduplicated journal after applying ALL currently delivered messages. Write inventory.json with batch (maximum N, initially zero), unique_events, groups map keyed sku@warehouse containing on_hand,reserved,available. Never read batch files before their messages arrive; do not poll or wait: complete after each currently available update, and subsequent updates resume the same task. Read and verify artifacts before finishing.`, filepath.ToSlash(c.dir))
	contract += " IMPORTANT lifecycle: this invocation must RETURN A FINAL ASSISTANT RESPONSE after processing the seed or all currently delivered batches. NEVER call inbox_wait in this inventory task. The host will start another invocation of the same persistent task for later messages; you do not need to stay alive to receive them. After writing and verifying inventory.json, return INVENTORY_BATCH_COMMITTED and end immediately."
	task := activeSubmit(t, c.ctx, router, mailbox.NewUserMessage(contract))
	complexCompleted(t, c.ctx, results, task)
	c.settled(state, 1)
	closeActive(t, set)
	// All nine batches arrive while the receiver is offline, deliberately out of order.
	var originals []protocols.Message
	for _, i := range []int{4, 1, 7, 2, 6, 3, 8, 5, 9} {
		m, err := peer.Send(c.ctx, space.ID, protocols.SendMessage{Content: map[string]any{"text": fmt.Sprintf("BATCH=%d PATH=batch-%d.json. Merge all delivered updates using the existing contract and preserve highest event revisions.", i, i)}, Refs: &protocols.Ref{Nodes: []string{node}}, Meta: harness.Envelope{Version: harness.Version, Event: harness.Message, Source: harness.Address{SessionID: "warehouse-stream"}, Target: harness.Address{TaskID: task}}.Meta()})
		if err != nil {
			t.Fatal(err)
		}
		originals = append(originals, m)
	}
	_, set, connection = open()
	if connection.Service().Client().NodeID() != node {
		t.Fatal("offline identity changed")
	}
	for range originals {
		waitFor(t, c.ctx, wires)
	}
	// Wait for actual durable drain, not for the first intermediate completion.
	c.settled(state, 1)
	routeRecords := func() []protocols.Message {
		t.Helper()
		records, readErr := connection.Service().Client().Read(c.ctx, space.ID, protocols.ReadOptions{All: true})
		if readErr != nil {
			t.Fatal(readErr)
		}
		var routed []protocols.Message
		for _, record := range records {
			envelope, parseErr := harness.Parse(record.Meta)
			if parseErr != nil {
				t.Fatal(parseErr)
			}
			if envelope != nil && envelope.Event == harness.InboxRoute {
				routed = append(routed, record)
			}
		}
		return routed
	}
	routes := routeRecords()
	if len(routes) != 10 {
		t.Fatalf("IOA must record initial task handoff and each of 9 routed batches; got %d", len(routes))
	}
	routeIDs, inputIDs := map[string]bool{}, map[string]bool{}
	for _, record := range routes {
		detail, parseErr := harness.Parse(record.Meta)
		if parseErr != nil || detail == nil || detail.Target.TaskID != task {
			t.Fatalf("handoff lost target task identity: %#v", record.Meta)
		}
		id := detail.ID
		if detail.CallID != "" {
			t.Fatalf("inbox handoff invented a parent tool call: %#v", detail)
		}
		if id == "" || routeIDs[id] || len(record.Refs.Messages) > 1 {
			t.Fatalf("handoff identity missing or repeated: %#v", record.Meta)
		}
		routeIDs[id] = true
		if len(record.Refs.Messages) == 1 {
			inputID := record.Refs.Messages[0]
			if detail.Source.NodeID != peer.NodeID() || detail.Source.SessionID != "warehouse-stream" {
				t.Fatalf("router lost remote inventory provenance: %#v", detail.Source)
			}
			if inputID == "" || inputIDs[inputID] {
				t.Fatalf("handoff source reference missing or repeated: %#v", record.Refs)
			}
			inputIDs[inputID] = true
		}
	}
	for _, original := range originals {
		if !inputIDs[original.ID] {
			t.Fatalf("IOA handoff did not retain source message %s", original.ID)
		}
	}
	type balance struct {
		OnHand    int `json:"on_hand"`
		Reserved  int `json:"reserved"`
		Available int `json:"available"`
	}
	latest := map[string]event{}
	for _, e := range all {
		if latest[e.ID].Revision < e.Revision {
			latest[e.ID] = e
		}
	}
	want := map[string]balance{}
	for _, e := range latest {
		k := e.SKU + "@" + e.Warehouse
		v := want[k]
		switch e.Kind {
		case "receive":
			v.OnHand += e.Quantity
		case "ship":
			v.OnHand -= e.Quantity
		case "reserve":
			v.Reserved += e.Quantity
		case "release":
			v.Reserved -= e.Quantity
		}
		v.Available = v.OnHand - v.Reserved
		want[k] = v
	}
	var actual struct {
		Batch  int
		Unique int `json:"unique_events"`
		Groups map[string]balance
	}
	before := c.read("inventory.json", &actual)
	if actual.Batch != 9 || actual.Unique != len(latest) || !reflect.DeepEqual(actual.Groups, want) {
		t.Fatalf("inventory oracle mismatch: %+v want %v", actual, want)
	}
	var journal struct {
		MaxBatch int `json:"max_batch"`
		Events   []event
	}
	c.read("journal.json", &journal)
	seenEvents := map[string]event{}
	for _, e := range journal.Events {
		if _, ok := seenEvents[e.ID]; ok {
			t.Fatalf("journal repeated event %s", e.ID)
		}
		seenEvents[e.ID] = e
	}
	if journal.MaxBatch != 9 || !reflect.DeepEqual(seenEvents, latest) {
		t.Fatalf("journal lost highest revision: %+v", journal)
	}
	if c.full.Load() == 0 {
		t.Fatal("scenario did not exercise IOA backpressure")
	}
	if c.peak.Load() != 1 {
		t.Fatalf("same inventory task executed concurrently: %d", c.peak.Load())
	}
	closeActive(t, set)
	router, set, _ = open()
	calls := c.calls.Load()
	for _, m := range originals {
		duplicate := mailbox.NewMessage(mailbox.OriginPeer, "user", "duplicate transport replay")
		duplicate.Message.Id = m.ID
		duplicate.Meta = mailbox.Metadata{MessageID: m.ID, Sender: peer.NodeID(), SourceSessionID: "warehouse-stream", TaskID: task}
		if activeSubmit(t, c.ctx, router, duplicate) != task {
			t.Fatal("duplicate forked task")
		}
	}
	c.settled(state, 1)
	if c.calls.Load() != calls {
		t.Fatal("duplicate replay invoked real model")
	}
	if after := routeRecords(); len(after) != len(routes) {
		t.Fatalf("duplicate transport replay wrote %d additional IOA handoffs", len(after)-len(routes))
	}
	after, err := os.ReadFile(filepath.Join(c.dir, "inventory.json"))
	if err != nil || string(after) != string(before) {
		t.Fatal("duplicate replay changed inventory")
	}
	db, err := sql.Open("sqlite", filepath.Join(state, "active.sqlite"))
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	var inputs int
	if err = db.QueryRow("SELECT count(*) FROM inputs").Scan(&inputs); err != nil || inputs != 10 {
		t.Fatalf("inputs=%d expected10: %v", inputs, err)
	}
	transcript, readErr := connection.Service().Client().Read(c.ctx, space.ID, protocols.ReadOptions{All: true})
	if readErr != nil {
		t.Fatal(readErr)
	}
	c.write("ioa-messages.json", transcript)
	c.write("oracle.json", map[string]any{"groups": want, "unique_events": len(latest), "batch": 9, "accepted_inputs": inputs, "backpressure_rejections": c.full.Load(), "duplicate_replay_model_calls": c.calls.Load() - calls})
}

func TestLiveComplexIOAReleaseAckLoss(t *testing.T) {
	c := newComplexLive(t, "release-ack-loss")
	files := map[string]string{}
	for i := 0; i < 12; i++ {
		name := fmt.Sprintf("source-%02d.txt", i)
		body := strings.Repeat(fmt.Sprintf("module-%d: dependency evidence\n", i), i+2)
		files[name] = body
		if err := os.WriteFile(filepath.Join(c.dir, name), []byte(body), 0600); err != nil {
			t.Fatal(err)
		}
	}
	deps := map[string]any{"packages": map[string]any{"a": map[string]any{"deps": []string{"b", "d"}, "license": "MIT", "severity": "none"}, "b": map[string]any{"deps": []string{"c"}, "license": "MIT", "severity": "none"}, "c": map[string]any{"deps": []string{}, "license": "MIT", "severity": "high"}, "d": map[string]any{"deps": []string{"e"}, "license": "Apache-2.0", "severity": "none"}, "e": map[string]any{"deps": []string{}, "license": "MIT", "severity": "low"}, "f": map[string]any{"deps": []string{"e"}, "license": "GPL-3.0", "severity": "none"}}, "services": map[string][]string{"api": {"a", "d"}, "billing": {"b", "e"}, "admin": {"f"}, "public": {"d"}}}
	c.write("dependencies.json", deps)
	store := ioaserver.NewMemoryStore()
	defer store.Close()
	server := httptest.NewServer(ioaserver.NewHTTPHandler(ioaserver.NewService(store, "attestation")))
	defer server.Close()
	endpoint := strings.Replace(server.URL, "http://", "http://attestation@", 1)
	peer, err := peerclient.NewClient(endpoint, "")
	if err != nil {
		t.Fatal(err)
	}
	if err = peer.EnsureRegistered(c.ctx, "attestation-producer", "", nil); err != nil {
		t.Fatal(err)
	}
	space, err := peer.Space(c.ctx, "attestation", "release provenance")
	if err != nil {
		t.Fatal(err)
	}
	results := make(chan mailbox.Message, 16)
	wires := make(chan mailbox.Message, 16)
	receiverState := filepath.Join(c.dir, "receiver-state")
	config := inboxext.Config{StateDir: receiverState, MaxActive: 1, Instructions: complexRouting, Notify: func(_ context.Context, m mailbox.Message) error { results <- m; return nil }}
	openReceiver := func() (*inboxext.Router, *extension.Set, *ioaclient.Extension) {
		return complexIOAHost(t, c, c.model, config, endpoint, "attestation", func(m mailbox.Message) {
			if m.Meta.Kind == "task_result" {
				wires <- m
			}
		})
	}
	router, receiverSet, connection := openReceiver()
	defer func() { closeActive(t, receiverSet) }()
	node := connection.Service().Client().NodeID()
	collector := activeSubmit(t, c.ctx, router, mailbox.NewUserMessage(fmt.Sprintf(`ROLE=provenance-auditor. Work only in %q using bash/Python; no subagents. You own audit.json and audit.log. Initially there is no manifest: just finish saying waiting for producer, without writing anything. When a task_result arrives, read actual manifest.json, all source-*.txt files and dependencies.json. Independently recalculate every file's byte length and SHA-256, total_bytes, and blocked_services: any transitive dependency with severity high/critical OR license GPL-3.0 blocks its service. Verify exact manifest matches. Write audit.json with verified=true, manifest_sha256 (hash of raw manifest bytes), total_bytes, blocked_services (sorted names). Append exactly one line VERIFIED to audit.log only after successful verification. Do not modify inputs or manifest. Inspect actual files before claiming success.`, filepath.ToSlash(c.dir))))
	complexCompleted(t, c.ctx, results, collector)
	c.settled(receiverState, 1)
	var sends, producerCalls atomic.Int32
	var allowRetry atomic.Bool
	lost := make(chan string, 1)
	feedbackIDs := make(chan string, 16)
	producerModel := modelFunc(func(ctx context.Context, r *provider.ChatCompletionRequest) (*provider.ChatCompletionResponse, error) {
		producerCalls.Add(1)
		return c.model.ChatCompletion(ctx, r)
	})
	producerState := filepath.Join(c.dir, "producer-state")
	producerConfig := inboxext.Config{StateDir: producerState, MaxActive: 1, Instructions: complexRouting, Notify: func(ctx context.Context, m mailbox.Message) error {
		if sends.Load() > 0 && !allowRetry.Load() {
			return errors.New("delivery unavailable until restart")
		}
		_, err := peer.Send(ctx, space.ID, protocols.SendMessage{Content: map[string]any{"text": provider.MessageText(m.Message)}, Refs: &protocols.Ref{Nodes: []string{node}}, Meta: harness.Envelope{Version: harness.Version, Event: harness.TaskResult, ID: m.Message.Id, Source: harness.Address{SessionID: "manifest-producer"}, Target: harness.Address{TaskID: collector}}.Meta()})
		if err != nil {
			return err
		}
		feedbackIDs <- m.Message.Id
		if sends.Add(1) == 1 {
			select {
			case r := <-results:
				if r.Meta.Status != "completed" || r.Meta.TaskID != collector {
					return fmt.Errorf("auditor failed: %v", r.Meta)
				}
			case <-ctx.Done():
				return ctx.Err()
			}
			lost <- m.Message.Id
			return errors.New("injected ACK loss after recipient committed audit")
		}
		return nil
	}}
	_, producer, _, producerSet := activeHost(t, producerModel, producerConfig)
	defer func() { closeActive(t, producerSet) }()
	producerTask := activeSubmit(t, c.ctx, producer, mailbox.NewUserMessage(fmt.Sprintf(`ROLE=release-manifest. In %q use bash/Python to hash the exact raw bytes of all and only source-*.txt files. Write manifest.json with files (map filename to {sha256,size}), total_bytes, blocked_services (sorted service names). Read dependencies.json and walk each service's transitive package dependencies; a high or critical vulnerability or GPL-3.0 license blocks its service. No subagents, no external services, no input or audit file modifications. Re-read and verify manifest before finishing with RELEASE_MANIFEST_READY. This result will be routed to the independent auditor.`, filepath.ToSlash(c.dir))))
	firstID := waitFor(t, c.ctx, lost)
	first := waitFor(t, c.ctx, wires)
	closeActive(t, producerSet)
	c.settled(receiverState, 1)
	closeActive(t, receiverSet)
	_, receiverSet, connection = openReceiver()
	if connection.Service().Client().NodeID() != node {
		t.Fatal("auditor identity changed")
	}
	before := producerCalls.Load()
	allowRetry.Store(true)
	_, _, _, producerSet = activeHost(t, producerModel, producerConfig)
	second := waitFor(t, c.ctx, wires)
	retry1, retry2 := waitFor(t, c.ctx, feedbackIDs), waitFor(t, c.ctx, feedbackIDs)
	if first.Message.Id == second.Message.Id || retry1 != retry2 || firstID != retry1 || first.Meta.FeedbackID != retry1 || second.Meta.FeedbackID != retry1 {
		t.Fatalf("retry identities incorrect: %v %v", first.Meta, second.Meta)
	}
	c.settled(producerState, 1)
	c.settled(receiverState, 1)
	if producerCalls.Load() != before {
		t.Fatal("outbox retry reexecuted producer/model")
	}
	type fileDigest struct {
		SHA256 string `json:"sha256"`
		Size   int    `json:"size"`
	}
	want := map[string]fileDigest{}
	total := 0
	for name, body := range files {
		sum := sha256.Sum256([]byte(body))
		want[name] = fileDigest{hex.EncodeToString(sum[:]), len(body)}
		total += len(body)
	}
	var manifest struct {
		Files   map[string]fileDigest
		Total   int      `json:"total_bytes"`
		Blocked []string `json:"blocked_services"`
	}
	raw := c.read("manifest.json", &manifest)
	if !reflect.DeepEqual(manifest.Files, want) || manifest.Total != total || !reflect.DeepEqual(manifest.Blocked, []string{"admin", "api", "billing"}) {
		t.Fatalf("manifest oracle mismatch: %+v", manifest)
	}
	var audit struct {
		Verified bool
		SHA      string   `json:"manifest_sha256"`
		Total    int      `json:"total_bytes"`
		Blocked  []string `json:"blocked_services"`
	}
	c.read("audit.json", &audit)
	sum := sha256.Sum256(raw)
	log, err := os.ReadFile(filepath.Join(c.dir, "audit.log"))
	if err != nil || strings.TrimSpace(string(log)) != "VERIFIED" || !audit.Verified || audit.SHA != hex.EncodeToString(sum[:]) || audit.Total != total || !reflect.DeepEqual(audit.Blocked, manifest.Blocked) {
		t.Fatalf("auditor mismatch or repeated effect: audit=%+v log=%q err=%v", audit, log, err)
	}
	transcript, readErr := connection.Service().Client().Read(c.ctx, space.ID, protocols.ReadOptions{All: true})
	if readErr != nil {
		t.Fatal(readErr)
	}
	c.write("ioa-messages.json", transcript)
	forwarded := 0
	for _, message := range transcript {
		record, parseErr := harness.Parse(message.Meta)
		if parseErr != nil {
			t.Fatal(parseErr)
		}
		if record == nil || record.Event != harness.InboxRoute || len(message.Refs.Messages) == 0 {
			continue
		}
		if len(message.Refs.Messages) != 1 || message.Refs.Messages[0] != first.Message.Id || record.Source.NodeID != peer.NodeID() || record.Source.SessionID != "manifest-producer" || record.Target.TaskID != collector {
			t.Fatalf("retry route lost original producer provenance: record=%#v refs=%#v", record, message.Refs)
		}
		forwarded++
	}
	if forwarded != 1 {
		t.Fatalf("ACK-loss retry must retain one admitted source route, got %d", forwarded)
	}
	c.write("oracle.json", map[string]any{"producer_task": producerTask, "collector_task": collector, "total_bytes": total, "blocked_services": manifest.Blocked, "feedback_id": retry1, "transport_ids": []string{first.Message.Id, second.Message.Id}, "sends": sends.Load(), "producer_reexecution_calls": producerCalls.Load() - before, "audit_entries": 1, "source_routes": forwarded})
}
