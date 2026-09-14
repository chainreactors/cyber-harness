package session

import (
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/aop"
	"google.golang.org/protobuf/proto"
)

func TestQueuedCommandsCannotMutateSessionHistoryInPlace(t *testing.T) {
	for _, command := range []string{"/clear", "/compact", "/compact focus"} {
		t.Run(command, func(t *testing.T) {
			manager := newBareRuntime(t, nil, nil)
			session, err := manager.OpenSession(t.Context(), SessionOptions{
				ID: "history",
				Messages: []*aop.Message{
					{Role: "user", Content: []*aop.Content{aop.Text("preserve this history")}},
				},
			})
			if err != nil {
				t.Fatal(err)
			}
			before := session.MessagesSnapshot()
			id := session.ID()
			outcome := session.currentState().commands.execute(t.Context(), command)
			if outcome.err == nil || !strings.Contains(outcome.err.Error(), "is not a Runtime command") {
				t.Fatalf("queued dispatcher still handles rotating command %q: %v", command, outcome.err)
			}
			after := session.MessagesSnapshot()
			if session.ID() != id || len(after) != 1 || !proto.Equal(before[0], after[0]) {
				t.Fatal("rejected command changed session identity or history")
			}
		})
	}
}
