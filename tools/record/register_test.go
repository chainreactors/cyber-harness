//go:build full && record_ffmpeg && cgo && (windows || linux)

package record

import "testing"

func TestConfiguredRecorderUsesConfiguration(t *testing.T) {
	recorder, err := NewConfigured(t.TempDir(), t.TempDir(), 3)
	if err != nil {
		t.Fatal(err)
	}
	defer recorder.Close()
	if recorder.maxConcurrent != 3 {
		t.Fatalf("maximum: %d", recorder.maxConcurrent)
	}
}

func TestConfiguredRecorderRejectsInvalidConfiguration(t *testing.T) {
	if recorder, err := NewConfigured(t.TempDir(), t.TempDir(), 17); err == nil {
		recorder.Close()
		t.Fatal("accepted invalid maximum")
	}
}
