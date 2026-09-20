package engine

import (
	"context"
	"errors"
	"testing"
)

func TestCanceledSprayInvocationRetainsGateUntilUpstreamCloses(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	release, err := AcquireSpray(ctx)
	if err != nil {
		t.Fatal(err)
	}
	input := make(chan int)
	output := forwardResults(ctx, input, func(v int) (int, bool) { return v, true }, release)
	cancel()
	// The forwarding goroutine must still consume upstream's trailing output.
	input <- 1
	waiting, stop := context.WithCancel(context.Background())
	stop()
	if _, err := AcquireSpray(waiting); !errors.Is(err, context.Canceled) {
		t.Fatalf("admission: %v", err)
	}
	select {
	case <-output:
		t.Fatal("public stream closed before upstream drained")
	default:
	}
	close(input)
	for range output {
	}
	next, err := AcquireSpray(t.Context())
	if err != nil {
		t.Fatal(err)
	}
	next()
}
