//go:build full && record_ffmpeg && cgo && (windows || linux)

package record

import "testing"

func TestConfiguredRecorderUsesEnvironment(t *testing.T) {
	t.Setenv(maxConcurrentEnv, "3")
	recorder, err := NewConfigured(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	if recorder.maxConcurrent != 3 {
		t.Fatalf("maximum: %d", recorder.maxConcurrent)
	}
}

func TestConfiguredRecorderRejectsInvalidEnvironment(t *testing.T) {
	t.Setenv(maxConcurrentEnv, "17")
	if recorder, err := NewConfigured(t.TempDir()); err == nil {
		recorder.Close()
		t.Fatal("accepted invalid maximum")
	}
}
