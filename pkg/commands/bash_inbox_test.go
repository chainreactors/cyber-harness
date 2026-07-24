package commands

import (
	"context"
	"io"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/inbox"
)

func TestBashBackgroundMonitorUsesInvocationInbox(t *testing.T) {
	tool := NewBashTool(t.TempDir(), 5)
	defer tool.Close()
	scoped := inbox.NewBuffered(8)
	defer scoped.Close()

	release := make(chan struct{})
	info, err := tool.tasks.CreateFunc(context.Background(), "scoped-inbox", 5*time.Second, func(context.Context, io.Writer) error {
		<-release
		return nil
	})
	if err != nil {
		t.Fatal(err)
	}
	tool.startMonitor(info, scoped)
	close(release)

	deadline := time.Now().Add(2 * time.Second)
	received := false
	for time.Now().Before(deadline) {
		if len(scoped.Drain()) > 0 {
			received = true
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if !received {
		t.Fatal("scoped inbox did not receive background completion")
	}
}
