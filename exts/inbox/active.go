package inbox

import (
	"bufio"
	"context"
	"database/sql"
	"errors"
	"fmt"
	"io"
	"slices"
	"strings"
	"sync"
	"time"

	"github.com/chainreactors/cyber/agent"
	mailbox "github.com/chainreactors/cyber/agent/inbox"
	"github.com/chainreactors/cyber/agent/provider"
	"github.com/chainreactors/cyber/agent/session"
	"github.com/chainreactors/cyber/agent/subagent"
	"github.com/chainreactors/cyber/agent/subagent/sessionexec"
	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/eventbus"
	"github.com/chainreactors/cyber/core/events"
	"github.com/chainreactors/cyber/core/extension"
	"github.com/chainreactors/cyber/core/hooks"
	"github.com/chainreactors/cyber/core/operation"
	procbus "github.com/chainreactors/cyber/core/proc"
	toolhooks "github.com/chainreactors/cyber/core/tool/hooks"
	"github.com/chainreactors/utils/proc"
)

type activeExecution struct {
	task     taskRecord
	procID   string
	inputs   []taskInput
	ready    bool
	settling bool
	jobs     []string
}
type activeRuntime struct {
	extension  *Extension
	store      *activeStore
	dispatcher *sessionexec.Tool
	manager    *procbus.Manager
	agent      *agent.Agent
	ctx        context.Context
	cancel     context.CancelFunc
	mu         sync.Mutex
	running    map[string]*activeExecution
	wake       chan struct{}
	work       chan struct{}
	notify     chan struct{}
	notified   chan struct{}
	done       chan struct{}
	err        error
	closeOnce  sync.Once
	closeErr   error
	observer   *eventbus.Subscription[*aop.Event]
	owners     map[string]string
	readers    sync.WaitGroup
}

func (e *Extension) loadActive(scope *extension.Scope, ag *agent.Agent, providers *provider.State, stream *events.Stream) error {
	dispatcher, err := extension.Use[*sessionexec.Tool](scope)
	if err != nil {
		return err
	}
	manager, err := extension.Use[*procbus.Manager](scope)
	if err != nil {
		return err
	}
	store, err := openActiveStore(e.config.StateDir)
	if err != nil {
		return err
	}
	ctx, cancel := context.WithCancel(scope.Lifetime())
	a := &activeRuntime{extension: e, store: store, dispatcher: dispatcher, manager: manager, agent: ag, ctx: ctx, cancel: cancel, running: make(map[string]*activeExecution), owners: make(map[string]string), wake: make(chan struct{}, 1), work: make(chan struct{}, 1), notify: make(chan struct{}, 1), notified: make(chan struct{}), done: make(chan struct{})}
	history, err := store.value("router_history")
	if err == nil {
		var messages []*aop.Message
		messages, err = decodeHistory(history)
		if err == nil {
			ag.LoadMessages(messages)
		}
	}
	if err != nil {
		cancel()
		_ = store.close()
		return err
	}
	registry, err := extension.Use[*hooks.Registry](scope)
	if err != nil {
		cancel()
		_ = store.close()
		return err
	}
	a.observer = stream.Observe(a.observe)
	e.subs = append(e.subs, toolhooks.ProcessStartedObserved.On(registry, "inbox.task-jobs", a.processStarted))
	e.router.mu.Lock()
	e.router.active = a
	e.router.ctx = ctx
	e.router.mu.Unlock()
	info, err := manager.Start(ctx, proc.Spec{Name: "router", Kind: "agent", Command: "inbox router"}, proc.IOFunc(func(unit context.Context, r io.Reader, w io.Writer) error {
		stop := context.AfterFunc(unit, cancel)
		defer stop()
		a.readers.Add(1)
		go a.readInput(unit, r, "")
		ticker := time.NewTicker(250 * time.Millisecond)
		defer ticker.Stop()
		for {
			receipt, readErr := store.next()
			if readErr == nil {
				e.router.mu.Lock()
				if existing := e.router.receipts[receipt.key]; existing != nil {
					receipt = existing
				} else {
					e.router.receipts[receipt.key] = receipt
				}
				e.router.mu.Unlock()
				e.route(ag, providers, stream, receipt)
				if ctx.Err() != nil {
					break
				}
				_, _ = fmt.Fprintf(w, "message=%s routed\n", receipt.ID)
				continue
			}
			if !errors.Is(readErr, sql.ErrNoRows) {
				a.fail(readErr)
				break
			}
			select {
			case <-ctx.Done():
				return nil
			case <-a.wake:
			case <-ticker.C:
			}
		}
		return nil
	}, nil))
	if err != nil {
		cancel()
		e.router.mu.Lock()
		e.router.active = nil
		e.router.mu.Unlock()
		a.observer.Cancel()
		_ = a.observer.Close(context.Background())
		_ = store.close()
		return err
	}
	e.done = make(chan struct{})
	go func() {
		defer close(e.done)
		defer ag.Cfg.Inbox.Close()
		defer stream.Publish(&aop.Event{SessionId: e.router.sessionID, Emitter: "inbox", Payload: &aop.Event_SessionEnded{SessionEnded: &aop.SessionEnded{Reason: "router_closed"}}})
		<-ctx.Done()
		// Join the registry unit before closing durable state.
		_, _ = manager.Wait(context.Background(), info.ID, 0)
		e.router.mu.Lock()
		for _, r := range e.router.receipts {
			r.once.Do(func() { r.err = ctx.Err(); close(r.done) })
		}
		e.router.mu.Unlock()
	}()
	go a.schedule()
	go a.deliverResults()
	a.signal()
	return nil
}
func (a *activeRuntime) signal() {
	select {
	case a.wake <- struct{}{}:
	default:
	}
	select {
	case a.work <- struct{}{}:
	default:
	}
	select {
	case a.notify <- struct{}{}:
	default:
	}
}
func (a *activeRuntime) fail(err error) {
	if err == nil {
		return
	}
	a.mu.Lock()
	a.err = errors.Join(a.err, err)
	a.mu.Unlock()
	a.cancel()
}
func (a *activeRuntime) readInput(ctx context.Context, r io.Reader, taskID string) {
	defer a.readers.Done()
	scanner := bufio.NewScanner(r)
	scanner.Buffer(make([]byte, 4096), 1024*1024)
	for scanner.Scan() {
		if strings.TrimSpace(scanner.Text()) == "" {
			continue
		}
		message := mailbox.NewUserMessage(scanner.Text())
		if taskID != "" {
			message.Meta = map[string]any{"task_id": taskID}
		}
		for {
			_, err := a.extension.router.Submit(ctx, message)
			if err == nil {
				break
			}
			if !errors.Is(err, mailbox.ErrInboxFull) {
				return
			}
			select {
			case <-ctx.Done():
				return
			case <-time.After(20 * time.Millisecond):
			}
		}
	}
}
func (a *activeRuntime) schedule() {
	defer close(a.done)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	var workers sync.WaitGroup
	defer workers.Wait()
	for {
		if a.ctx.Err() != nil {
			return
		}
		tasks, err := a.store.tasks()
		if err != nil {
			a.fail(err)
			return
		}
		for _, t := range tasks {
			if t.State == "running" {
				a.deliverLive(t.ID)
				continue
			}
			if t.State != "queued" && t.State != "interrupted" {
				continue
			}
			a.mu.Lock()
			if a.running[t.ID] != nil || len(a.running) >= a.extension.config.MaxActive {
				a.mu.Unlock()
				continue
			}
			t.SessionID = "execution-" + aop.EnvelopeID()
			// Leave one mailbox slot for the interrupted-execution warning.
			inputs, history, err := a.store.claim(t, t.SessionID, agent.DefaultInboxCapacity-1)
			if err != nil {
				a.mu.Unlock()
				if !errors.Is(err, sql.ErrNoRows) {
					a.fail(err)
				}
				continue
			}
			ex := &activeExecution{task: t, inputs: inputs}
			a.running[t.ID] = ex
			a.owners[t.SessionID] = t.ID
			a.mu.Unlock()
			workers.Add(1)
			info, startErr := a.manager.Start(a.ctx, proc.Spec{Name: t.ID, Kind: "agent", Command: "subagent " + t.ID}, proc.IOFunc(func(ctx context.Context, r io.Reader, w io.Writer) error {
				defer workers.Done()
				defer func() {
					a.stopJobs(ex)
					a.mu.Lock()
					delete(a.running, t.ID)
					for id, owner := range a.owners {
						if owner == t.ID {
							delete(a.owners, id)
						}
					}
					a.mu.Unlock()
					a.signal()
				}()
				a.readers.Add(1)
				go a.readInput(ctx, r, t.ID)
				_, _ = fmt.Fprintf(w, "task=%s execution=%s\n", t.ID, t.SessionID)
				messages := make([]mailbox.Message, len(inputs))
				for i, input := range inputs {
					messages[i] = input.message
				}
				prompt := provider.MessageText(messages[0].Message)
				if t.State == "interrupted" {
					note := mailbox.NewSystemMessage("Execution was interrupted. Continue this task from its restored history. Completed tools may already have changed files or external systems; reconcile their current state before repeating actions. The working directory is shared with other tasks.")
					note.Message.Id = "recovery-" + t.SessionID
					messages = append(messages, note)
				}
				checkpointed := false
				_, runErr := a.dispatcher.Dispatch(ctx, subagent.Request{Name: t.Name, Label: t.Label, Mode: subagent.Async, Input: subagent.Input{Prompt: prompt}},
					session.SessionOptions{ID: t.SessionID, Messages: history, HistorySnapshot: len(history) > 0}, messages,
					func(snapshot []*aop.Message, result *agent.Result, runErr error) error {
						a.mu.Lock()
						defer a.mu.Unlock()
						ex.settling = true
						checkpointed = true
						status := "completed"
						if runErr != nil {
							status = "failed"
						}
						if ctx.Err() != nil {
							status = "canceled"
						}
						if a.ctx.Err() != nil {
							status = "interrupted"
						}
						consumed := make(map[string]bool)
						// Compaction can remove consumed envelopes from the final
						// snapshot. Retain both restored and newly consumed identities.
						for _, m := range history {
							consumed[m.Id] = true
						}
						for _, m := range snapshot {
							consumed[m.Id] = true
						}
						if result != nil {
							for _, m := range result.NewMessages {
								consumed[m.Id] = true
							}
						}
						selected := make([]taskInput, 0, len(ex.inputs))
						for _, input := range ex.inputs {
							// A failed request may leave accepted live inputs unread.
							// Only explicit cancellation abandons those inputs.
							if consumed[input.message.Message.Id] || status == "canceled" {
								selected = append(selected, input)
							}
						}
						var feedback *mailbox.Message
						if status != "interrupted" {
							text := ""
							if result != nil {
								text = result.Output
							}
							if runErr != nil {
								text = fmt.Sprintf("%v\n%s", runErr, text)
							}
							m := mailbox.NewMessage(mailbox.OriginSystem, "user", text)
							m.Message.Id = "feedback-" + t.SessionID
							m.Meta = map[string]any{"kind": "subagent_feedback", "task_id": t.ID, "execution_id": t.SessionID, "status": status}
							m.Meta["feedback_chain"] = feedbackChain(selected, t.ID)
							feedback = &m
							_, _ = fmt.Fprintln(w, text)
						}
						err := a.store.complete(t, selected, snapshot, status, feedback)
						if err != nil {
							go a.fail(err)
						}
						return err
					})
				if !checkpointed {
					a.mu.Lock()
					ex.settling = true
					pending := append([]taskInput(nil), ex.inputs...)
					a.mu.Unlock()
					status := "failed"
					if a.ctx.Err() != nil {
						status = "interrupted"
					} else if ctx.Err() != nil {
						status = "canceled"
					}
					m := mailbox.NewMessage(mailbox.OriginSystem, "user", fmt.Sprint(runErr))
					m.Message.Id = "feedback-" + t.SessionID
					m.Meta = map[string]any{"kind": "subagent_feedback", "task_id": t.ID, "execution_id": t.SessionID, "status": status}
					m.Meta["feedback_chain"] = feedbackChain(pending, t.ID)
					var feedback *mailbox.Message
					if status != "interrupted" {
						feedback = &m
					}
					if err := a.store.complete(t, pending, history, status, feedback); err != nil {
						a.fail(err)
					}
				}
				return runErr
			}, nil))
			if startErr != nil {
				workers.Done()
				a.mu.Lock()
				delete(a.running, t.ID)
				a.mu.Unlock()
				if err := a.store.complete(t, inputs, history, "interrupted", nil); err != nil {
					a.fail(err)
				}
			} else {
				a.mu.Lock()
				ex.procID = info.ID
				a.mu.Unlock()
				if _, err := a.store.db.Exec("UPDATE attempts SET proc_id=? WHERE session_id=?", info.ID, t.SessionID); err != nil {
					a.fail(err)
				}
			}
		}
		select {
		case <-a.ctx.Done():
			return
		case <-a.work:
		case <-ticker.C:
		}
	}
}

// Forward already persisted corrections only while this exact execution is
// accepting input. Any input not present in the checkpoint is queued again.
func (a *activeRuntime) deliverLive(taskID string) {
	a.mu.Lock()
	defer a.mu.Unlock()
	ex := a.running[taskID]
	if ex == nil || !ex.ready || ex.settling {
		return
	}
	receiver, err := a.extension.router.resolve(ex.task.SessionID)
	if err != nil {
		return
	}
	rows, err := a.store.db.Query("SELECT seq,body FROM inputs WHERE task_id=? AND state='pending' ORDER BY seq", taskID)
	if err != nil {
		go a.fail(err)
		return
	}
	var pending []taskInput
	for rows.Next() {
		var input taskInput
		var b []byte
		if err = rows.Scan(&input.seq, &b); err != nil {
			break
		}
		input.message, err = decodeMessage(b)
		if err != nil {
			break
		}
		pending = append(pending, input)
	}
	err = errors.Join(err, rows.Err(), rows.Close())
	if err != nil {
		go a.fail(err)
		return
	}
	for _, input := range pending {
		if err = receiver.deliver(a.ctx, cloneMessage(input.message)); err != nil {
			return
		}
		if _, err = a.store.db.Exec("UPDATE inputs SET state='running',session_id=? WHERE seq=?", ex.task.SessionID, input.seq); err != nil {
			go a.fail(err)
			return
		}
		ex.inputs = append(ex.inputs, input)
	}
}
func (a *activeRuntime) reply(r *Receipt) error {
	id, _ := r.message.Meta["task_id"].(string)
	var source []byte
	if err := a.store.db.QueryRow("SELECT source FROM tasks WHERE id=?", id).Scan(&source); err != nil {
		return err
	}
	original, err := decodeMessage(source)
	if err != nil {
		return err
	}
	output := cloneMessage(r.message)
	output.Meta["reply_node"] = original.Meta["sender"]
	output.Meta["reply_session"] = original.Meta["source_session_id"]
	output.Meta["reply_message"] = original.Meta["message_id"]
	if output.Meta["reply_message"] == nil {
		output.Meta["reply_message"] = original.Message.Id
	}
	output.Meta["space"] = original.Meta["space"]
	b, err := encodeMessage(output)
	if err != nil {
		return err
	}
	_, err = a.store.db.Exec("INSERT OR IGNORE INTO outbox(id,body) VALUES(?,?)", r.ID, b)
	a.signal()
	return err
}
func (a *activeRuntime) sendResults() error {
	if a.extension.config.Notify == nil {
		return nil
	}
	for {
		var id string
		var b []byte
		err := a.store.db.QueryRow("SELECT id,body FROM outbox LEFT JOIN retries ON retries.message_key='outbox:'||outbox.id WHERE state='pending' AND COALESCE(due,0)<=? ORDER BY outbox.rowid LIMIT 1", time.Now().UnixMilli()).Scan(&id, &b)
		if errors.Is(err, sql.ErrNoRows) {
			return nil
		}
		if err != nil {
			return err
		}
		message, err := decodeMessage(b)
		if err != nil {
			return err
		}
		if err = a.extension.config.Notify(a.ctx, message); err != nil {
			if a.ctx.Err() != nil {
				return nil
			}
			a.agent.Cfg.Logger.Warnf("active result %s delivery will retry: %v", id, err)
			if _, err = a.store.db.Exec("INSERT OR REPLACE INTO retries(message_key,attempts,due) VALUES(?,1,?)", "outbox:"+id, time.Now().Add(time.Second).UnixMilli()); err != nil {
				return err
			}
			continue
		}
		if _, err = a.store.db.Exec("UPDATE outbox SET state='sent' WHERE id=?", id); err != nil {
			return err
		}
		if _, err = a.store.db.Exec("DELETE FROM retries WHERE message_key=?", "outbox:"+id); err != nil {
			return err
		}
	}
}

func (a *activeRuntime) deliverResults() {
	defer close(a.notified)
	ticker := time.NewTicker(time.Second)
	defer ticker.Stop()
	for a.ctx.Err() == nil {
		if err := a.sendResults(); err != nil {
			a.fail(err)
			return
		}
		select {
		case <-a.ctx.Done():
			return
		case <-a.notify:
		case <-ticker.C:
		}
	}
}

// Source cursors share the active-mode durability and directory ownership.
func (r *Router) LoadCursor(key string) (string, error) {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil || r.ctx.Err() != nil {
		return "", ErrUnavailable
	}
	b, err := r.active.store.value("source:" + key)
	return string(b), err
}
func (r *Router) SaveCursor(key, value string) error {
	r.mu.Lock()
	defer r.mu.Unlock()
	if r.active == nil || r.ctx.Err() != nil {
		return ErrUnavailable
	}
	return r.active.store.setValue("source:"+key, []byte(value))
}
func (r *Router) Done() <-chan struct{} { return r.ctx.Done() }

func (a *activeRuntime) close(ctx context.Context) error {
	a.cancel()
	select {
	case <-a.done:
	case <-ctx.Done():
		return ctx.Err()
	}
	select {
	case <-a.notified:
	case <-ctx.Done():
		return ctx.Err()
	}
	a.mu.Lock()
	err := a.err
	a.mu.Unlock()
	a.readers.Wait()
	a.observer.Cancel()
	if closeErr := a.observer.Close(ctx); closeErr != nil {
		return errors.Join(err, closeErr)
	}
	a.closeOnce.Do(func() { a.closeErr = a.store.close() })
	return errors.Join(err, a.closeErr)
}

// The canonical stream is synchronous. Commit complete input/model/tool
// records before the producer can advance; diagnostic deltas are not recovery.
func (a *activeRuntime) observe(event *aop.Event) {
	a.mu.Lock()
	owner := a.owners[event.SessionId]
	if started := event.GetSessionStarted(); started != nil && owner == "" {
		if parent := a.owners[started.ParentSessionId]; parent != "" {
			a.owners[event.SessionId] = parent
		}
	}
	ex := a.running[owner]
	root := ex != nil && ex.task.SessionID == event.SessionId
	if root && event.GetTurnStarted() != nil {
		// SessionStart precedes seed admission. Wait for the actual run before
		// allowing live input to compete for the execution's mailbox capacity.
		ex.ready = true
		a.signal()
	}
	a.mu.Unlock()
	// Restored snapshot records have no turn; the seed is already in tasks.history.
	if !root || event.TurnId == "" || (event.GetMessage() == nil && event.GetToolResult() == nil) {
		return
	}
	if err := a.store.journal(event); err != nil {
		a.fail(err)
	}
}

func (a *activeRuntime) processStarted(ctx context.Context, event toolhooks.ProcessEvent) (struct{}, error) {
	a.mu.Lock()
	defer a.mu.Unlock()
	if ex := a.running[a.owners[operation.InvocationFromContext(ctx).SessionID]]; ex != nil && event.Operation.GetResourceId() != "" {
		ex.jobs = append(ex.jobs, event.Operation.ResourceId)
	}
	return struct{}{}, nil
}

func (a *activeRuntime) stopJobs(ex *activeExecution) {
	a.mu.Lock()
	jobs := append([]string(nil), ex.jobs...)
	a.mu.Unlock()
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	for _, id := range jobs {
		_ = a.manager.Stop(id, proc.StopOptions{Reason: "task execution ended"})
		_, _ = a.manager.Wait(ctx, id, 0)
	}
}

func feedbackChain(inputs []taskInput, taskID string) []string {
	chain := []string{}
	for _, input := range inputs {
		for _, id := range messageChain(input.message) {
			if !slices.Contains(chain, id) {
				chain = append(chain, id)
			}
		}
	}
	if !slices.Contains(chain, taskID) {
		chain = append(chain, taskID)
	}
	return chain
}

func messageChain(message mailbox.Message) []string {
	var chain []string
	switch values := message.Meta["feedback_chain"].(type) {
	case []string:
		chain = append(chain, values...)
	case []any:
		for _, value := range values {
			if id, ok := value.(string); ok {
				chain = append(chain, id)
			}
		}
	}
	if id, _ := message.Meta["task_id"].(string); id != "" && message.Meta["kind"] == "subagent_feedback" && !slices.Contains(chain, id) {
		chain = append(chain, id)
	}
	return chain
}
