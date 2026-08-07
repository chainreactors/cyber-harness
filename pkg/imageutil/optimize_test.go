package imageutil

import (
	"bytes"
	"image"
	"testing"
)

func TestOptimizeImageResizesToPayloadBounds(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 4000, 1000))
	optimized, err := OptimizeImage(img)
	if err != nil {
		t.Fatal(err)
	}
	if optimized.OrigW != 4000 || optimized.OrigH != 1000 {
		t.Fatalf("original dimensions = %dx%d", optimized.OrigW, optimized.OrigH)
	}
	if optimized.FinalW != MaxDimension || optimized.FinalH != 500 {
		t.Fatalf("final dimensions = %dx%d, want %dx500", optimized.FinalW, optimized.FinalH, MaxDimension)
	}
	if len(optimized.Data) == 0 || len(optimized.Data) > MaxPayloadBytes {
		t.Fatalf("optimized payload size = %d", len(optimized.Data))
	}
}

func TestOptimizePassesThroughUnknownImageData(t *testing.T) {
	raw := []byte("not-an-image")
	optimized, err := Optimize(bytes.NewReader(raw), "application/octet-stream")
	if err != nil {
		t.Fatal(err)
	}
	if optimized.MimeType != "application/octet-stream" || !bytes.Equal(optimized.Data, raw) {
		t.Fatalf("unexpected passthrough result: %+v", optimized)
	}
}

func TestOptimizeRejectsOversizedPassthrough(t *testing.T) {
	_, err := Optimize(bytes.NewReader(make([]byte, MaxPayloadBytes+1)), "image/gif")
	if err == nil {
		t.Fatal("expected oversized passthrough error")
	}
}
