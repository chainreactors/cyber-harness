package record

import (
	"context"
	"encoding/json"
	"errors"
	"image"
	"image/color"
	"math"
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"sync"
	"testing"
	"time"

	coretool "github.com/chainreactors/aiscan/core/tool"
)

type fakeBackend struct {
	mu            sync.Mutex
	startOnce     sync.Once
	started       chan struct{}
	resolveErr    error
	screenshot    image.Image
	screenshotErr error
	nilScreenshot bool
	record        func(context.Context, string) (mediaInfo, error)
}

func (b *fakeBackend) Resolve(_ context.Context, req captureRequest) (resolvedTarget, error) {
	if b.resolveErr != nil {
		return resolvedTarget{}, b.resolveErr
	}
	info := TargetInfo{Kind: req.Target, PID: req.PID, Width: 320, Height: 240}
	if req.WindowHandle != 0 {
		info.WindowHandle = "0x1"
	}
	return resolvedTarget{Info: info}, nil
}

func (b *fakeBackend) Screenshot(context.Context, resolvedTarget) (image.Image, error) {
	if b.screenshotErr != nil {
		return nil, b.screenshotErr
	}
	if b.nilScreenshot {
		return nil, nil
	}
	if b.screenshot != nil {
		return b.screenshot, nil
	}
	img := image.NewRGBA(image.Rect(0, 0, 320, 240))
	img.Set(1, 1, color.RGBA{R: 255, A: 255})
	return img, nil
}

func (b *fakeBackend) Record(ctx context.Context, _ resolvedTarget, output string, _ int) (mediaInfo, error) {
	b.mu.Lock()
	if b.started != nil {
		b.startOnce.Do(func() { close(b.started) })
	}
	b.mu.Unlock()
	if b.record != nil {
		return b.record(ctx, output)
	}
	<-ctx.Done()
	if err := os.WriteFile(output, []byte("fake-mp4"), 0o644); err != nil {
		return mediaInfo{}, err
	}
	return mediaInfo{Width: 320, Height: 240, Frames: 3}, nil
}

func TestScreenshotReturnsImageAndPath(t *testing.T) {
	dir := t.TempDir()
	tool := New(dir, filepath.Join(dir, "record"), 4, &fakeBackend{})
	result, err := tool.Execute(context.Background(), `{"action":"screenshot"}`)
	if err != nil {
		t.Fatal(err)
	}
	if !coretool.ResultHasImages(result) {
		t.Fatal("screenshot result should contain an image")
	}
	text := coretool.ResultText(result)
	var meta struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(text), &meta); err != nil {
		t.Fatal(err)
	}
	if _, err := os.Stat(meta.Output); err != nil {
		t.Fatalf("screenshot output: %v", err)
	}
}

func TestAsyncStartStopStatus(t *testing.T) {
	dir := t.TempDir()
	backend := &fakeBackend{started: make(chan struct{})}
	tool := New(dir, filepath.Join(dir, "record"), 4, backend)
	result, err := tool.Execute(context.Background(), `{"action":"start","target":"window","window_handle":"0x1"}`)
	if err != nil {
		t.Fatal(err)
	}
	var info SessionInfo
	if err := json.Unmarshal([]byte(coretool.ResultText(result)), &info); err != nil {
		t.Fatal(err)
	}
	select {
	case <-backend.started:
	case <-time.After(time.Second):
		t.Fatal("recording did not start")
	}
	stopArgs := `{"action":"stop","recording_id":"` + info.RecordingID + `"}`
	result, err = tool.Execute(context.Background(), stopArgs)
	if err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(coretool.ResultText(result)), &info); err != nil {
		t.Fatal(err)
	}
	if info.State != "completed" || info.Frames != 3 || info.Bytes == 0 {
		t.Fatalf("unexpected final info: %+v", info)
	}
	statusArgs := `{"action":"status","recording_id":"` + info.RecordingID + `"}`
	result, err = tool.Execute(context.Background(), statusArgs)
	if err != nil || !strings.Contains(coretool.ResultText(result), `"state": "completed"`) {
		t.Fatalf("status result=%q err=%v", coretool.ResultText(result), err)
	}
}

func TestConcurrencyLimit(t *testing.T) {
	dir := t.TempDir()
	tool := New(dir, filepath.Join(dir, "record"), 2, &fakeBackend{})
	for i := 0; i < 2; i++ {
		if _, err := tool.Execute(context.Background(), `{"action":"start"}`); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := tool.Execute(context.Background(), `{"action":"start"}`); err == nil || !strings.Contains(err.Error(), "concurrency limit 2") {
		t.Fatalf("expected concurrency error, got %v", err)
	}
	tool.Close()
}

func TestValidation(t *testing.T) {
	tool := New(t.TempDir(), t.TempDir(), 4, &fakeBackend{})
	tests := []string{
		`{"action":"record"}`,
		`{"action":"screenshot","target":"window"}`,
		`{"action":"screenshot","target":"window","pid":1,"window_handle":"1"}`,
		`{"action":"screenshot","target":"window","pid":4294967296}`,
		`{"action":"screenshot","fps":61}`,
		`{"action":"stop"}`,
	}
	for _, input := range tests {
		if _, err := tool.Execute(context.Background(), input); err == nil {
			t.Errorf("expected error for %s", input)
		}
	}
}

func TestRelativeOutputUsesInvocationWorkDir(t *testing.T) {
	dir := t.TempDir()
	tool := New("ignored", filepath.Join(dir, "default"), 4, &fakeBackend{})
	ctx := coretool.ContextWithInvocation(context.Background(), coretool.Invocation{WorkDir: dir})
	result, err := tool.Execute(ctx, `{"action":"screenshot","output":"shots/test.png"}`)
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(coretool.ResultText(result)), &meta); err != nil {
		t.Fatal(err)
	}
	if meta.Output != filepath.Join(dir, "shots", "test.png") {
		t.Fatalf("result path = %s", meta.Output)
	}
}

func TestDefaultOutputUsesInvocationRecordDir(t *testing.T) {
	dir := t.TempDir()
	tool := New("ignored", filepath.Join(t.TempDir(), "fallback"), 4, &fakeBackend{})
	ctx := coretool.ContextWithInvocation(context.Background(), coretool.Invocation{WorkDir: dir})
	result, err := tool.Execute(ctx, `{"action":"screenshot"}`)
	if err != nil {
		t.Fatal(err)
	}
	var meta struct {
		Output string `json:"output"`
	}
	if err := json.Unmarshal([]byte(coretool.ResultText(result)), &meta); err != nil {
		t.Fatal(err)
	}
	wantDir := filepath.Join(dir, ".aiscan", "record")
	if filepath.Dir(meta.Output) != wantDir {
		t.Fatalf("result directory = %s, want %s", filepath.Dir(meta.Output), wantDir)
	}
}

func TestSynchronousRecordCompletesAfterDuration(t *testing.T) {
	dir := t.TempDir()
	recorder := New(dir, filepath.Join(dir, "record"), 1, &fakeBackend{})
	result, err := recorder.Execute(context.Background(), `{"action":"record","duration_seconds":0.01}`)
	if err != nil {
		t.Fatal(err)
	}
	var info SessionInfo
	if err := json.Unmarshal([]byte(coretool.ResultText(result)), &info); err != nil {
		t.Fatal(err)
	}
	if info.State != sessionCompleted || info.Frames != 3 || info.Bytes == 0 {
		t.Fatalf("unexpected final info: %+v", info)
	}
	if info.DurationMS < 1 {
		t.Fatalf("duration_ms = %d, want positive duration", info.DurationMS)
	}
	if !coretool.ResultHasMedia(result) {
		t.Fatal("record result should contain video media")
	}
	media := result.Output[1].GetMedia()
	if media == nil || media.Kind != "video" || media.Resource.GetMediaType() != "video/mp4" || media.Resource.GetUri() == "" {
		t.Fatalf("video media = %+v", media)
	}
	if filepath.IsAbs(media.Resource.GetUri()) {
		t.Fatalf("video URI should be relative to the invocation workdir: %q", media.Resource.GetUri())
	}
}

func TestBackendFailureReturnsStructuredToolError(t *testing.T) {
	backend := &fakeBackend{record: func(context.Context, string) (mediaInfo, error) {
		return mediaInfo{}, errors.New("encoder failed")
	}}
	recorder := New(t.TempDir(), t.TempDir(), 1, backend)
	result, err := recorder.Execute(context.Background(), `{"action":"record","duration_seconds":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if !result.GetIsError() {
		t.Fatalf("result should be marked as an error: %s", coretool.ResultText(result))
	}
	var info SessionInfo
	if err := json.Unmarshal([]byte(coretool.ResultText(result)), &info); err != nil {
		t.Fatal(err)
	}
	if info.State != sessionFailed || !strings.Contains(info.Error, "encoder failed") {
		t.Fatalf("unexpected failed session: %+v", info)
	}
}

func TestBackendPanicIsContainedInSession(t *testing.T) {
	backend := &fakeBackend{record: func(context.Context, string) (mediaInfo, error) {
		panic("native crash")
	}}
	recorder := New(t.TempDir(), t.TempDir(), 1, backend)
	result, err := recorder.Execute(context.Background(), `{"action":"record","duration_seconds":1}`)
	if err != nil {
		t.Fatal(err)
	}
	if !result.GetIsError() || !strings.Contains(coretool.ResultText(result), "capture backend panicked: native crash") {
		t.Fatalf("panic was not converted to a session error: %s", coretool.ResultText(result))
	}
}

func TestConcurrentStartAdmissionIsAtomic(t *testing.T) {
	recorder := New(t.TempDir(), t.TempDir(), 1, &fakeBackend{})
	t.Cleanup(recorder.Close)
	const attempts = 16
	var wg sync.WaitGroup
	errs := make(chan error, attempts)
	for range attempts {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := recorder.Execute(context.Background(), `{"action":"start"}`)
			errs <- err
		}()
	}
	wg.Wait()
	close(errs)
	successes := 0
	for err := range errs {
		if err == nil {
			successes++
			continue
		}
		if !strings.Contains(err.Error(), "concurrency limit 1") {
			t.Errorf("unexpected start error: %v", err)
		}
	}
	if successes != 1 {
		t.Fatalf("successful starts = %d, want 1", successes)
	}
}

func TestDuplicateActiveOutputIsRejected(t *testing.T) {
	dir := t.TempDir()
	recorder := New(dir, filepath.Join(dir, "record"), 2, &fakeBackend{})
	t.Cleanup(recorder.Close)
	if _, err := recorder.Execute(context.Background(), `{"action":"start","output":"same.mp4"}`); err != nil {
		t.Fatal(err)
	}
	if _, err := recorder.Execute(context.Background(), `{"action":"start","output":"same.mp4"}`); err == nil || !strings.Contains(err.Error(), "already used") {
		t.Fatalf("expected duplicate output error, got %v", err)
	}
}

func TestSessionHistoryIsBounded(t *testing.T) {
	backend := &fakeBackend{record: func(_ context.Context, output string) (mediaInfo, error) {
		if err := os.WriteFile(output, []byte("mp4"), 0o644); err != nil {
			return mediaInfo{}, err
		}
		return mediaInfo{Frames: 1, Width: 2, Height: 2}, nil
	}}
	recorder := New(t.TempDir(), t.TempDir(), 1, backend)
	for range maxSessionHistory + 5 {
		session, err := recorder.start(context.Background(), Args{}, 0)
		if err != nil {
			t.Fatal(err)
		}
		<-session.done
	}
	recorder.mu.RLock()
	count := len(recorder.sessions)
	recorder.mu.RUnlock()
	if count != maxSessionHistory {
		t.Fatalf("retained sessions = %d, want %d", count, maxSessionHistory)
	}
}

func TestClosedToolRejectsNewCaptureButKeepsStatus(t *testing.T) {
	recorder := New(t.TempDir(), t.TempDir(), 1, &fakeBackend{})
	recorder.Close()
	if _, err := recorder.Execute(context.Background(), `{"action":"screenshot"}`); err == nil || !strings.Contains(err.Error(), "closed") {
		t.Fatalf("expected closed error, got %v", err)
	}
	if _, err := recorder.Execute(context.Background(), `{"action":"status"}`); err != nil {
		t.Fatalf("status after close: %v", err)
	}
}

func TestUnavailableOrInvalidScreenshotBackend(t *testing.T) {
	for name, backend := range map[string]captureBackend{
		"nil backend":   nil,
		"backend error": &fakeBackend{screenshotErr: errNilImage},
		"nil image":     &fakeBackend{nilScreenshot: true},
	} {
		t.Run(name, func(t *testing.T) {
			recorder := New(t.TempDir(), t.TempDir(), 1, backend)
			_, err := recorder.Execute(context.Background(), `{"action":"screenshot"}`)
			if err == nil {
				t.Fatal("expected screenshot error")
			}
		})
	}
}

var errNilImage = errors.New("no frame")

func TestNormalizeDuration(t *testing.T) {
	tests := []struct {
		name     string
		seconds  float64
		required bool
		wantErr  string
	}{
		{name: "optional zero", seconds: 0},
		{name: "required zero", seconds: 0, required: true, wantErr: "greater than zero"},
		{name: "negative", seconds: -1, wantErr: "cannot be negative"},
		{name: "nan", seconds: math.NaN(), wantErr: "finite"},
		{name: "infinity", seconds: math.Inf(1), wantErr: "finite"},
		{name: "overflow", seconds: float64(math.MaxInt64), wantErr: "too large"},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := normalizeDuration(tt.seconds, tt.required)
			if tt.wantErr == "" && err != nil {
				t.Fatal(err)
			}
			if tt.wantErr != "" && (err == nil || !strings.Contains(err.Error(), tt.wantErr)) {
				t.Fatalf("error = %v, want substring %q", err, tt.wantErr)
			}
		})
	}
}

func TestSamePathUsesPlatformCaseSemantics(t *testing.T) {
	a := filepath.Join(t.TempDir(), "Capture.mp4")
	b := filepath.Join(filepath.Dir(a), "capture.mp4")
	got := samePath(a, b)
	if runtime.GOOS == "windows" && !got {
		t.Fatal("Windows paths should compare case-insensitively")
	}
	if runtime.GOOS != "windows" && got {
		t.Fatal("non-Windows paths should compare case-sensitively")
	}
}
