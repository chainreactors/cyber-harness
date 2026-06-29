package engine

import (
	"context"
	"sync/atomic"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/neutron/operators"
	neutronhttp "github.com/chainreactors/neutron/protocols/http"
	"github.com/chainreactors/neutron/templates"
	"github.com/chainreactors/sdk/gogo"
	"github.com/chainreactors/sdk/neutron"
	sdktypes "github.com/chainreactors/sdk/pkg/types"
	"github.com/chainreactors/sdk/spray"
	sdkzombie "github.com/chainreactors/sdk/zombie"
)

// ---------------------------------------------------------------------------
// 1. NewEngine with nil config must succeed (returns usable engine)
// ---------------------------------------------------------------------------

func TestGogoNewEngineNilConfig(t *testing.T) {
	eng, err := gogo.NewEngine(nil)
	if err != nil {
		t.Fatalf("gogo.NewEngine(nil) error = %v", err)
	}
	if eng == nil {
		t.Fatal("gogo.NewEngine(nil) returned nil engine")
	}
	defer eng.Close()
}

func TestSprayNewEngineNilConfig(t *testing.T) {
	eng, err := spray.NewEngine(nil)
	if err != nil {
		t.Fatalf("spray.NewEngine(nil) error = %v", err)
	}
	if eng == nil {
		t.Fatal("spray.NewEngine(nil) returned nil engine")
	}
	defer eng.Close()
}

func TestZombieNewEngineNilConfig(t *testing.T) {
	eng, err := sdkzombie.NewEngine(nil)
	if err != nil {
		t.Fatalf("zombie.NewEngine(nil) error = %v", err)
	}
	if eng == nil {
		t.Fatal("zombie.NewEngine(nil) returned nil engine")
	}
}

func TestNeutronNewEngineNilConfig(t *testing.T) {
	eng, err := neutron.NewEngine(nil)
	if err != nil {
		t.Fatalf("neutron.NewEngine(nil) error = %v", err)
	}
	if eng == nil {
		t.Fatal("neutron.NewEngine(nil) returned nil engine")
	}
	defer eng.Close()
}

// ---------------------------------------------------------------------------
// 2. Execute rejects nil context (interface nil)
// ---------------------------------------------------------------------------

func TestGogoExecuteRejectsNilContext(t *testing.T) {
	eng, err := gogo.NewEngine(nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()

	_, err = eng.Execute(nil, gogo.NewScanTask("127.0.0.1", "80"))
	if err == nil {
		t.Fatal("Execute(nil, task) should return error")
	}
	if err.Error() != "nil context" {
		t.Fatalf("error = %q, want 'nil context'", err)
	}
}

func TestSprayExecuteRejectsNilContext(t *testing.T) {
	eng, err := spray.NewEngine(nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()

	_, err = eng.Execute(nil, spray.NewCheckTask([]string{"http://127.0.0.1"}))
	if err == nil {
		t.Fatal("Execute(nil, task) should return error")
	}
	if err.Error() != "nil context" {
		t.Fatalf("error = %q, want 'nil context'", err)
	}
}

func TestNeutronExecuteRejectsNilContext(t *testing.T) {
	eng, err := neutron.NewEngineWithTemplates(
		(neutron.Templates{}).Merge([]*templates.Template{testNeutronTemplate("test-nil-ctx")}),
	)
	if err != nil {
		t.Fatalf("NewEngineWithTemplates: %v", err)
	}
	defer eng.Close()

	_, err = eng.Execute(nil, neutron.NewExecuteTask("http://127.0.0.1"))
	if err == nil {
		t.Fatal("Execute(nil, task) should return error")
	}
	if err.Error() != "nil context" {
		t.Fatalf("error = %q, want 'nil context'", err)
	}
}

func TestZombieExecuteRejectsNilContext(t *testing.T) {
	eng, err := sdkzombie.NewEngine(nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	_, err = eng.Execute(nil, sdkzombie.NewBruteTask([]sdkzombie.Target{{IP: "127.0.0.1", Port: "22", Service: "ssh"}}))
	if err == nil {
		t.Fatal("Execute(nil, task) should return error")
	}
	if err.Error() != "nil context" {
		t.Fatalf("error = %q, want 'nil context'", err)
	}
}

// ---------------------------------------------------------------------------
// 3. Execute rejects typed nil context (*Context passed as interface)
// ---------------------------------------------------------------------------

func TestGogoExecuteRejectsTypedNilContext(t *testing.T) {
	eng, err := gogo.NewEngine(nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()

	var ctx *gogo.Context
	_, err = eng.Execute(ctx, gogo.NewScanTask("127.0.0.1", "80"))
	if err == nil {
		t.Fatal("Execute(typed-nil, task) should return error")
	}
}

func TestSprayExecuteRejectsTypedNilContext(t *testing.T) {
	eng, err := spray.NewEngine(nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()

	var ctx *spray.Context
	_, err = eng.Execute(ctx, spray.NewCheckTask([]string{"http://127.0.0.1"}))
	if err == nil {
		t.Fatal("Execute(typed-nil, task) should return error")
	}
}

func TestNeutronExecuteRejectsTypedNilContext(t *testing.T) {
	eng, err := neutron.NewEngineWithTemplates(
		(neutron.Templates{}).Merge([]*templates.Template{testNeutronTemplate("test-typed-nil-ctx")}),
	)
	if err != nil {
		t.Fatalf("NewEngineWithTemplates: %v", err)
	}
	defer eng.Close()

	var ctx *neutron.Context
	_, err = eng.Execute(ctx, neutron.NewExecuteTask("http://127.0.0.1"))
	if err == nil {
		t.Fatal("Execute(typed-nil, task) should return error")
	}
}

func TestZombieExecuteRejectsTypedNilContext(t *testing.T) {
	eng, err := sdkzombie.NewEngine(nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	var ctx *sdkzombie.Context
	_, err = eng.Execute(ctx, sdkzombie.NewBruteTask([]sdkzombie.Target{{IP: "127.0.0.1", Port: "22", Service: "ssh"}}))
	if err == nil {
		t.Fatal("Execute(typed-nil, task) should return error")
	}
}

// ---------------------------------------------------------------------------
// 4. NewContext().WithContext(ctx) properly propagates context
// ---------------------------------------------------------------------------

func TestGogoContextPropagatesCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	gogoCtx := gogo.NewContext().WithContext(ctx)
	if gogoCtx.Context().Err() == nil {
		t.Fatal("canceled context not propagated to gogo Context")
	}
}

func TestSprayContextPropagatesCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	sprayCtx := spray.NewContext().WithContext(ctx)
	if sprayCtx.Context().Err() == nil {
		t.Fatal("canceled context not propagated to spray Context")
	}
}

func TestNeutronContextPropagatesCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	nCtx := neutron.NewContext().WithContext(ctx)
	if nCtx.Context().Err() == nil {
		t.Fatal("canceled context not propagated to neutron Context")
	}
}

func TestZombieContextPropagatesCancel(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	cancel()

	zCtx := sdkzombie.NewContext().WithContext(ctx)
	if zCtx.Context().Err() == nil {
		t.Fatal("canceled context not propagated to zombie Context")
	}
}

// ---------------------------------------------------------------------------
// 5. Nil receiver Context does not panic
// ---------------------------------------------------------------------------

func TestGogoNilReceiverContext(t *testing.T) {
	var ctx *gogo.Context
	got := ctx.Context()
	if got == nil {
		t.Fatal("nil receiver Context() returned nil, want context.Background()")
	}
}

func TestSprayNilReceiverContext(t *testing.T) {
	var ctx *spray.Context
	got := ctx.Context()
	if got == nil {
		t.Fatal("nil receiver Context() returned nil, want context.Background()")
	}
}

func TestNeutronNilReceiverContext(t *testing.T) {
	var ctx *neutron.Context
	got := ctx.Context()
	if got == nil {
		t.Fatal("nil receiver Context() returned nil, want context.Background()")
	}
}

func TestZombieNilReceiverContext(t *testing.T) {
	var ctx *sdkzombie.Context
	got := ctx.Context()
	if got == nil {
		t.Fatal("nil receiver Context() returned nil, want context.Background()")
	}
}

// ---------------------------------------------------------------------------
// 6. WithContext(nil) on nil receiver creates valid context (does not panic)
// ---------------------------------------------------------------------------

func TestGogoWithContextNilReceiver(t *testing.T) {
	var ctx *gogo.Context
	got := ctx.WithContext(context.Background())
	if got == nil {
		t.Fatal("nil receiver WithContext returned nil")
	}
	if got.Context() == nil {
		t.Fatal("resulting context is nil")
	}
}

func TestZombieWithContextNilReceiver(t *testing.T) {
	var ctx *sdkzombie.Context
	got := ctx.WithContext(context.Background())
	if got == nil {
		t.Fatal("nil receiver WithContext returned nil")
	}
	if got.Context() == nil {
		t.Fatal("resulting context is nil")
	}
}

// ---------------------------------------------------------------------------
// 7. aiscan wrapper: GogoScanStream rejects nil engine
// ---------------------------------------------------------------------------

func TestGogoScanStreamRejectsNilEngine(t *testing.T) {
	_, err := GogoScanStream(context.Background(), nil, GogoScanOptions{
		Target: "127.0.0.1",
		Ports:  "80",
	})
	if err == nil {
		t.Fatal("GogoScanStream(nil engine) should return error")
	}
}

func TestSprayCheckStreamRejectsNilEngine(t *testing.T) {
	_, err := SprayCheckStream(context.Background(), nil, SprayCheckOptions{
		URLs: []string{"http://127.0.0.1"},
	})
	if err == nil {
		t.Fatal("SprayCheckStream(nil engine) should return error")
	}
}

func TestNeutronExecuteStreamRejectsNilEngine(t *testing.T) {
	_, err := NeutronExecuteStream(context.Background(), nil, nil, NeutronExecuteOptions{
		Target: "http://127.0.0.1",
	})
	if err == nil {
		t.Fatal("NeutronExecuteStream(nil engine) should return error")
	}
}

func TestZombieWeakpassStreamRejectsNilEngine(t *testing.T) {
	_, err := ZombieWeakpassStream(context.Background(), nil, ZombieWeakpassOptions{
		Targets: []sdkzombie.Target{{IP: "127.0.0.1", Port: "22", Service: "ssh"}},
	})
	if err == nil {
		t.Fatal("ZombieWeakpassStream(nil engine) should return error")
	}
}

// ---------------------------------------------------------------------------
// 8. aiscan wrapper: context.Background() is properly passed to SDK engine
//    (verifies the NewContext().WithContext(ctx) chain does not produce nil)
// ---------------------------------------------------------------------------

func TestGogoScanStreamPassesContext(t *testing.T) {
	eng, err := gogo.NewEngine(nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	ch, err := GogoScanStream(ctx, eng, GogoScanOptions{
		Target:  "127.0.0.1",
		Ports:   "1",
		Threads: 1,
		Timeout: 1,
	})
	if err != nil {
		t.Fatalf("GogoScanStream error = %v", err)
	}
	for range ch {
	}
}

func TestSprayCheckStreamPassesContext(t *testing.T) {
	eng, err := spray.NewEngine(nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	ch, err := SprayCheckStream(ctx, eng, SprayCheckOptions{
		URLs:    []string{"http://127.0.0.1:1"},
		Threads: 1,
		Timeout: 1,
	})
	if err != nil {
		t.Fatalf("SprayCheckStream error = %v", err)
	}
	for range ch {
	}
}

func TestZombieWeakpassStreamPassesContext(t *testing.T) {
	eng, err := sdkzombie.NewEngine(nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 50*time.Millisecond)
	defer cancel()

	ch, err := ZombieWeakpassStream(ctx, eng, ZombieWeakpassOptions{
		Targets: []sdkzombie.Target{{IP: "127.0.0.1", Port: "1", Service: "ssh"}},
		Threads: 1,
		Timeout: 1,
	})
	if err != nil {
		t.Fatalf("ZombieWeakpassStream error = %v", err)
	}
	for range ch {
	}
}

// ---------------------------------------------------------------------------
// 9. Stats handler invocation does not panic after context cancel
// ---------------------------------------------------------------------------

func TestGogoStatsHandlerSafeAfterCancel(t *testing.T) {
	eng, err := gogo.NewEngine(nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()

	ctx, cancel := context.WithCancel(context.Background())

	var statsCalled atomic.Int32
	gogoCtx := gogo.NewContext().
		WithContext(ctx).
		SetThreads(1).
		SetStatsHandler(func(s sdktypes.Stats) {
			statsCalled.Add(1)
		})

	ch, err := eng.Execute(gogoCtx, gogo.NewScanTask("127.0.0.1", "1"))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	cancel()
	for range ch {
	}
}

func TestSprayStatsHandlerSafeAfterCancel(t *testing.T) {
	eng, err := spray.NewEngine(nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}
	defer eng.Close()

	ctx, cancel := context.WithCancel(context.Background())

	sprayCtx := spray.NewContext().
		WithContext(ctx).
		SetStatsHandler(func(s sdktypes.Stats) {})

	ch, err := eng.Execute(sprayCtx, spray.NewCheckTask([]string{"http://127.0.0.1:1"}))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	cancel()
	for range ch {
	}
}

func TestZombieStatsHandlerSafeAfterCancel(t *testing.T) {
	eng, err := sdkzombie.NewEngine(nil)
	if err != nil {
		t.Fatalf("NewEngine: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())

	zCtx := sdkzombie.NewContext().
		WithContext(ctx).
		SetThreads(1).
		SetTimeout(1).
		SetStatsHandler(func(s sdktypes.Stats) {})

	ch, err := eng.Execute(zCtx, sdkzombie.NewBruteTask([]sdkzombie.Target{{IP: "127.0.0.1", Port: "1", Service: "ssh"}}))
	if err != nil {
		t.Fatalf("Execute error = %v", err)
	}

	cancel()
	for range ch {
	}
}

// ---------------------------------------------------------------------------
// 10. aiscan wrapper context chain: verify the full
//     NewContext().WithContext(ctx) chain used in each wrapper
// ---------------------------------------------------------------------------

func TestGogoContextChainPreservesDeadline(t *testing.T) {
	deadline := time.Now().Add(5 * time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	gogoCtx := gogo.NewContext().WithContext(ctx)
	got, ok := gogoCtx.Context().Deadline()
	if !ok {
		t.Fatal("deadline not propagated")
	}
	if !got.Equal(deadline) {
		t.Fatalf("deadline = %v, want %v", got, deadline)
	}
}

func TestSprayContextChainPreservesDeadline(t *testing.T) {
	deadline := time.Now().Add(5 * time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	sprayCtx := spray.NewContext().WithContext(ctx)
	got, ok := sprayCtx.Context().Deadline()
	if !ok {
		t.Fatal("deadline not propagated")
	}
	if !got.Equal(deadline) {
		t.Fatalf("deadline = %v, want %v", got, deadline)
	}
}

func TestZombieContextChainPreservesDeadline(t *testing.T) {
	deadline := time.Now().Add(5 * time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	zCtx := sdkzombie.NewContext().WithContext(ctx)
	got, ok := zCtx.Context().Deadline()
	if !ok {
		t.Fatal("deadline not propagated")
	}
	if !got.Equal(deadline) {
		t.Fatalf("deadline = %v, want %v", got, deadline)
	}
}

func TestNeutronContextChainPreservesDeadline(t *testing.T) {
	deadline := time.Now().Add(5 * time.Minute)
	ctx, cancel := context.WithDeadline(context.Background(), deadline)
	defer cancel()

	nCtx := neutron.NewContext().WithContext(ctx)
	got, ok := nCtx.Context().Deadline()
	if !ok {
		t.Fatal("deadline not propagated")
	}
	if !got.Equal(deadline) {
		t.Fatalf("deadline = %v, want %v", got, deadline)
	}
}

// ---------------------------------------------------------------------------
// 11. telemetry.SDKGoRecover recovers from a panic in a goroutine
// ---------------------------------------------------------------------------

func TestSDKGoRecoverDoesNotCrash(t *testing.T) {
	done := make(chan struct{})
	go func() {
		defer telemetry.SDKGoRecover("test")
		defer close(done)
		panic("goroutine panic")
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("goroutine did not recover from panic")
	}
}

func testNeutronTemplate(id string) *templates.Template {
	return &templates.Template{
		Id: id,
		Info: templates.Info{
			Name:     id,
			Severity: "info",
		},
		RequestsHTTP: []*neutronhttp.Request{
			{
				Method: "GET",
				Path:   []string{"{{BaseURL}}"},
				Operators: operators.Operators{
					Matchers: []*operators.Matcher{
						{Type: "word", Words: []string{"definitely-not-present"}},
					},
				},
			},
		},
	}
}
