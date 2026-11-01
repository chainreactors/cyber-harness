package inbox

import (
	"context"
	"testing"

	mailbox "github.com/chainreactors/cyber/agent/inbox"
	aop "github.com/chainreactors/cyber/aop"
)

func TestRecoveredJournalIsRetiredWithSnapshot(t *testing.T) {
	dir := t.TempDir()
	store, err := openActiveStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.enqueue(&Receipt{ID: "initial", key: [4]string{"user", "", "", "initial"}, message: mailbox.NewUserMessage("initial task")}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.claim(taskRecord{ID: id}, "before-crash", 2); err != nil {
		t.Fatal(err)
	}
	if err := store.journal(&aop.Event{SessionId: "before-crash", Seq: 1, Payload: &aop.Event_Message{Message: &aop.Message{Id: "durable-message", Role: "assistant", Content: []*aop.Content{aop.Text("durable result")}}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	store, err = openActiveStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	inputs, history, err := store.claim(taskRecord{ID: id}, "after-crash", 2)
	if err != nil {
		t.Fatal(err)
	}
	if len(history) != 1 || history[0].Id != "durable-message" {
		t.Fatalf("recovery lost journal: %v", history)
	}
	if err := store.complete(taskRecord{ID: id, SessionID: "after-crash"}, inputs, history, "completed", nil); err != nil {
		t.Fatal(err)
	}
	var remaining int
	if err := store.db.QueryRow("SELECT count(*) FROM execution_events").Scan(&remaining); err != nil {
		t.Fatal(err)
	}
	if remaining != 0 {
		t.Fatalf("recovered journal retained forever after snapshot checkpoint: %d", remaining)
	}
}

func TestRecoveredJournalClaimRollbackPreservesEvidence(t *testing.T) {
	dir := t.TempDir()
	store, err := openActiveStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	id, err := store.enqueue(&Receipt{ID: "initial", key: [4]string{"user", "", "", "initial"}, message: mailbox.NewUserMessage("initial task")}, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.claim(taskRecord{ID: id}, "before-crash", 2); err != nil {
		t.Fatal(err)
	}
	if err := store.journal(&aop.Event{SessionId: "before-crash", Seq: 1, Payload: &aop.Event_Message{Message: &aop.Message{Id: "evidence", Role: "assistant", Content: []*aop.Content{aop.Text("evidence")}}}}); err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	store, err = openActiveStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	if _, err := store.db.Exec(`CREATE TRIGGER fail_claim BEFORE INSERT ON attempts BEGIN SELECT RAISE(ABORT, 'injected claim failure'); END`); err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.claim(taskRecord{ID: id}, "failed-claim", 2); err == nil {
		t.Fatal("fault injection did not fail the claim")
	}
	var sessionID, state string
	var count int
	if err := store.db.QueryRow("SELECT session_id,state FROM tasks WHERE id=?", id).Scan(&sessionID, &state); err != nil {
		t.Fatal(err)
	}
	if err := store.db.QueryRow("SELECT count(*) FROM execution_events").Scan(&count); err != nil {
		t.Fatal(err)
	}
	if sessionID != "before-crash" || state != "interrupted" || count != 1 {
		t.Fatalf("failed claim partially committed: session=%s state=%s journal=%d", sessionID, state, count)
	}
	if _, err := store.db.Exec("DROP TRIGGER fail_claim"); err != nil {
		t.Fatal(err)
	}
	_, history, err := store.claim(taskRecord{ID: id}, "successful-claim", 2)
	if err != nil || len(history) != 1 || history[0].Id != "evidence" {
		t.Fatalf("retry lost or duplicated evidence: history=%v err=%v", history, err)
	}
	if err := store.db.QueryRow("SELECT count(*) FROM execution_events").Scan(&count); err != nil || count != 0 {
		t.Fatalf("successful snapshot did not retire evidence: count=%d err=%v", count, err)
	}
}

func TestInterruptedTaskKeepsRecoveryStateWhenFollowupArrives(t *testing.T) {
	dir := t.TempDir()
	store, err := openActiveStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	initial := &Receipt{ID: "initial", key: [4]string{"user", "", "", "initial"}, message: mailbox.NewUserMessage("perform a durable action")}
	id, err := store.enqueue(initial, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, _, err := store.claim(taskRecord{ID: id}, "interrupted-execution", 2); err != nil {
		t.Fatal(err)
	}
	if err := store.close(); err != nil {
		t.Fatal(err)
	}
	store, err = openActiveStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	followup := &Receipt{ID: "followup", key: [4]string{"peer", "monitor", "", "followup"}, message: mailbox.NewUserMessage("new observation during recovery")}
	if _, err := store.enqueue(followup, id, "", ""); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.tasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 1 || tasks[0].State != "interrupted" {
		t.Fatalf("follow-up erased interrupted state and recovery warning: %+v", tasks)
	}
	if _, _, err := store.claim(tasks[0], "recovered-execution", 2); err != nil {
		t.Fatal(err)
	}
	var pending int
	if err := store.db.QueryRowContext(context.Background(), "SELECT count(*) FROM inputs WHERE task_id=? AND state='running'", id).Scan(&pending); err != nil {
		t.Fatal(err)
	}
	if pending != 2 {
		t.Fatalf("recovery lost inputs: %d", pending)
	}
}

func TestActiveTasksOrderByOldestPendingInput(t *testing.T) {
	store, err := openActiveStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer store.close()
	first := &Receipt{ID: "first", key: [4]string{"user", "", "", "first"}, message: mailbox.NewUserMessage("first task")}
	a, err := store.enqueue(first, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	second := &Receipt{ID: "second", key: [4]string{"user", "", "", "second"}, message: mailbox.NewUserMessage("second task")}
	b, err := store.enqueue(second, "", "", "")
	if err != nil {
		t.Fatal(err)
	}
	if _, err := store.db.Exec("UPDATE inputs SET state='done' WHERE task_id=?", a); err != nil {
		t.Fatal(err)
	}
	late := &Receipt{ID: "late", key: [4]string{"user", "", "", "late"}, message: mailbox.NewUserMessage("more work for first task")}
	if _, err := store.enqueue(late, a, "", ""); err != nil {
		t.Fatal(err)
	}
	tasks, err := store.tasks()
	if err != nil {
		t.Fatal(err)
	}
	if len(tasks) != 2 || tasks[0].ID != b || tasks[1].ID != a {
		t.Fatalf("older task jumped ahead of pending work: %+v", tasks)
	}
}
