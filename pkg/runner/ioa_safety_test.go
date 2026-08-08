package runner

import (
	"context"
	"testing"
	"time"

	inboxpkg "github.com/chainreactors/aiscan/agent/inbox"
	ioaclient "github.com/chainreactors/ioa/client"
)

func TestSubscribeIOASpaceTypedNilStreamReturns(t *testing.T) {
	var concrete *ioaclient.Client
	var stream ioaclient.StreamAPI = concrete
	done := make(chan struct{})
	go func() {
		subscribeIOASpace(context.Background(), stream, "space-1", "node-1", func(inboxpkg.Message) error {
			return nil
		}, nil)
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(time.Second):
		t.Fatal("typed-nil IOA stream did not return")
	}
}
