package imageutil

import (
	"bytes"
	"image"
	"image/color"
	"image/png"
	"testing"
)

func makeTestPNG(w, h int) []byte {
	img := image.NewRGBA(image.Rect(0, 0, w, h))
	for y := 0; y < h; y++ {
		for x := 0; x < w; x++ {
			img.Set(x, y, color.RGBA{
				R: uint8(x % 256),
				G: uint8(y % 256),
				B: uint8((x + y) % 256),
				A: 255,
			})
		}
	}
	var buf bytes.Buffer
	png.Encode(&buf, img)
	return buf.Bytes()
}

func TestOptimize_SmallImage_NoResize(t *testing.T) {
	raw := makeTestPNG(200, 100)
	opt, err := Optimize(bytes.NewReader(raw), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if opt.FinalW != 200 || opt.FinalH != 100 {
		t.Errorf("small image should not be resized, got %dx%d", opt.FinalW, opt.FinalH)
	}
}

func TestOptimize_LargeImage_Resized(t *testing.T) {
	raw := makeTestPNG(4000, 3000)
	opt, err := Optimize(bytes.NewReader(raw), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if opt.OrigW != 4000 || opt.OrigH != 3000 {
		t.Errorf("original dims wrong: %dx%d", opt.OrigW, opt.OrigH)
	}
	if opt.FinalW > MaxDimension || opt.FinalH > MaxDimension {
		t.Errorf("resized dims exceed max: %dx%d", opt.FinalW, opt.FinalH)
	}
	if opt.FinalW != 2000 || opt.FinalH != 1500 {
		t.Errorf("expected 2000x1500, got %dx%d", opt.FinalW, opt.FinalH)
	}
}

func TestOptimize_PicksSmallerFormat(t *testing.T) {
	raw := makeTestPNG(800, 600)
	opt, err := Optimize(bytes.NewReader(raw), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	if opt.MimeType != "image/png" && opt.MimeType != "image/jpeg" {
		t.Errorf("unexpected mime type: %s", opt.MimeType)
	}

	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		t.Fatal(err)
	}
	if got, want := len(opt.Data), min(len(EncodePNG(img)), len(EncodeJPEG(img, JPEGQualities[0]))); got != want {
		t.Fatalf("encoded size = %d, want smallest encoding %d", got, want)
	}
}

func TestOptimize_PayloadUnderLimit(t *testing.T) {
	raw := makeTestPNG(4000, 3000)
	opt, err := Optimize(bytes.NewReader(raw), "image/png")
	if err != nil {
		t.Fatal(err)
	}
	payloadSize := len(opt.Data)
	if payloadSize > MaxPayloadBytes {
		t.Errorf("payload %d exceeds limit %d", payloadSize, MaxPayloadBytes)
	}
}

func TestOptimize_GIF_Passthrough(t *testing.T) {
	// GIF should not be re-encoded
	raw := makeTestPNG(100, 100) // not a real GIF, but tests the passthrough path
	opt, err := Optimize(bytes.NewReader(raw), "image/gif")
	if err != nil {
		t.Fatal(err)
	}
	if opt.MimeType != "image/gif" {
		t.Errorf("GIF should pass through, got %s", opt.MimeType)
	}
}

func TestResizeIfNeeded_AspectRatio(t *testing.T) {
	img := image.NewRGBA(image.Rect(0, 0, 6000, 2000))
	resized := ResizeIfNeeded(img, 6000, 2000)
	b := resized.Bounds()
	if b.Dx() != 2000 {
		t.Errorf("width should be 2000, got %d", b.Dx())
	}
	expectedH := 2000 * 2000 / 6000 // 666
	if b.Dy() != expectedH {
		t.Errorf("height should be %d, got %d", expectedH, b.Dy())
	}
}
