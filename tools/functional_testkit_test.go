package tools

import (
	"bytes"
	"context"
	"fmt"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/chainreactors/aiscan/core/eventbus"
	"github.com/chainreactors/aiscan/core/output"
	"github.com/chainreactors/aiscan/pkg/commands"
)

type functionalResult struct {
	Stdout string
	Stderr string
	Events []output.ToolDataEvent
}

type functionalCase struct {
	Name          string
	Tool          string
	Args          []string
	Stdin         string
	Timeout       time.Duration
	SkipUnderRace bool
	Check         func(*testing.T, functionalResult)
}

type functionalRecorder struct {
	mu     sync.Mutex
	events []output.ToolDataEvent
}

func newFunctionalRecorder(bus *eventbus.Bus[output.ToolDataEvent]) *functionalRecorder {
	recorder := &functionalRecorder{}
	bus.Subscribe(func(event output.ToolDataEvent) {
		recorder.mu.Lock()
		recorder.events = append(recorder.events, event)
		recorder.mu.Unlock()
	})
	return recorder
}

func (r *functionalRecorder) mark() int {
	r.mu.Lock()
	defer r.mu.Unlock()
	return len(r.events)
}

func (r *functionalRecorder) since(mark int) []output.ToolDataEvent {
	r.mu.Lock()
	defer r.mu.Unlock()
	return append([]output.ToolDataEvent(nil), r.events[mark:]...)
}

func runFunctionalCases(t *testing.T, registry *commands.CommandRegistry, recorder *functionalRecorder, cases []functionalCase) {
	t.Helper()
	for _, testCase := range cases {
		t.Run(testCase.Name, func(t *testing.T) {
			if functionalRaceEnabled && testCase.SkipUnderRace {
				t.Skip("upstream scanner has a known internal race")
			}
			if !registry.Has(testCase.Tool) {
				t.Fatalf("tool %q is not registered", testCase.Tool)
			}
			timeout := testCase.Timeout
			if timeout <= 0 {
				timeout = 30 * time.Second
			}
			ctx, cancel := context.WithTimeout(context.Background(), timeout)
			defer cancel()

			var stdout, stderr bytes.Buffer
			parent := &commands.Execution{
				ID:        "functional-" + testCase.Name,
				Stdin:     strings.NewReader(testCase.Stdin),
				Stdout:    &stdout,
				Stderr:    &stderr,
				StartedAt: time.Now(),
			}
			mark := recorder.mark()
			_, err := registry.Run(ctx, append([]string{testCase.Tool}, testCase.Args...), parent)
			if err != nil {
				t.Fatalf("%s %s: %v\nstdout:\n%s\nstderr:\n%s", testCase.Tool, strings.Join(testCase.Args, " "), err, stdout.String(), stderr.String())
			}
			if err := ctx.Err(); err != nil {
				t.Fatalf("%s exceeded %s: %v", testCase.Tool, timeout, err)
			}
			result := functionalResult{Stdout: stdout.String(), Stderr: stderr.String(), Events: recorder.since(mark)}
			if testCase.Check != nil {
				testCase.Check(t, result)
			}
		})
	}
}

func requireFunctionalCoverage(t *testing.T, registry *commands.CommandRegistry, cases []functionalCase, coveredElsewhere ...string) {
	t.Helper()
	covered := make(map[string]bool, len(cases)+len(coveredElsewhere))
	for _, testCase := range cases {
		covered[testCase.Tool] = true
	}
	for _, name := range coveredElsewhere {
		covered[name] = true
	}
	for _, name := range registry.GroupNames("scanner") {
		if !covered[name] {
			t.Fatalf("scanner %q has no functional regression case", name)
		}
	}
}

func requireOutputContains(t *testing.T, result functionalResult, values ...string) {
	t.Helper()
	combined := result.Stdout + "\n" + result.Stderr
	for _, value := range values {
		if !strings.Contains(combined, value) {
			t.Fatalf("output missing %q\nstdout:\n%s\nstderr:\n%s", value, result.Stdout, result.Stderr)
		}
	}
}

func requireEvent(t *testing.T, result functionalResult, tool, kind string, match func(any) bool) output.ToolDataEvent {
	t.Helper()
	for _, event := range result.Events {
		if event.Tool == tool && event.Kind == kind && (match == nil || match(event.Data)) {
			return event
		}
	}
	t.Fatalf("missing event tool=%s kind=%s in %s", tool, kind, formatFunctionalEvents(result.Events))
	return output.ToolDataEvent{}
}

func formatFunctionalEvents(events []output.ToolDataEvent) string {
	var b strings.Builder
	for _, event := range events {
		fmt.Fprintf(&b, "{%s %s %s %T} ", event.Tool, event.Kind, event.Target, event.Data)
	}
	return b.String()
}

func writeTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.WriteFile(path, []byte(content), 0o600); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}
