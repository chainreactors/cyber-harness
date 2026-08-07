package imageutil

import (
	"bytes"
	"fmt"
	"image"
	"image/jpeg"
	"image/png"
	"io"

	"golang.org/x/image/draw"
	_ "golang.org/x/image/webp"
)

const (
	MaxDimension = 2000
	// Keeps inline media below a 4 MiB ProtoJSON WebSocket frame after base64
	// expansion and envelope overhead. Larger media travels by URI/file chunks.
	MaxPayloadBytes = 2_500_000
)

var JPEGQualities = []int{85, 70, 55, 40}

type Optimized struct {
	MimeType string
	Data     []byte
	OrigW    int
	OrigH    int
	FinalW   int
	FinalH   int
}

func Optimize(r io.Reader, srcMime string) (*Optimized, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}
	if srcMime == "image/gif" {
		return passthrough(raw, srcMime)
	}
	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return passthrough(raw, srcMime)
	}
	return optimizeImage(img)
}

func OptimizeImage(img image.Image) (*Optimized, error) {
	return optimizeImage(img)
}

func optimizeImage(img image.Image) (*Optimized, error) {
	bounds := img.Bounds()
	origW, origH := bounds.Dx(), bounds.Dy()
	img = ResizeIfNeeded(img, origW, origH)
	final := img.Bounds()
	data, mime, err := pickSmallestEncoding(img)
	if err != nil {
		return nil, err
	}
	return &Optimized{
		MimeType: mime,
		Data:     data,
		OrigW:    origW,
		OrigH:    origH,
		FinalW:   final.Dx(),
		FinalH:   final.Dy(),
	}, nil
}

func passthrough(raw []byte, mime string) (*Optimized, error) {
	if len(raw) > MaxPayloadBytes {
		return nil, fmt.Errorf("image too large after encoding (%d bytes, max %d)", len(raw), MaxPayloadBytes)
	}
	return &Optimized{MimeType: mime, Data: raw}, nil
}

func ResizeIfNeeded(img image.Image, w, h int) image.Image {
	if w <= MaxDimension && h <= MaxDimension {
		return img
	}
	var newW, newH int
	if w > h {
		newW = MaxDimension
		newH = h * MaxDimension / w
	} else {
		newH = MaxDimension
		newW = w * MaxDimension / h
	}
	if newW < 1 {
		newW = 1
	}
	if newH < 1 {
		newH = 1
	}
	dst := image.NewRGBA(image.Rect(0, 0, newW, newH))
	draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
	return dst
}

func pickSmallestEncoding(img image.Image) ([]byte, string, error) {
	pngData := EncodePNG(img)
	jpegData := EncodeJPEG(img, JPEGQualities[0])
	best, mime := pngData, "image/png"
	if len(jpegData) < len(best) {
		best, mime = jpegData, "image/jpeg"
	}
	if len(best) <= MaxPayloadBytes {
		return best, mime, nil
	}
	for _, quality := range JPEGQualities[1:] {
		jpegData = EncodeJPEG(img, quality)
		if len(jpegData) <= MaxPayloadBytes {
			return jpegData, "image/jpeg", nil
		}
	}
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	for w > 1 && h > 1 {
		w = max(1, w*3/4)
		h = max(1, h*3/4)
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
		jpegData = EncodeJPEG(dst, JPEGQualities[0])
		if len(jpegData) <= MaxPayloadBytes {
			return jpegData, "image/jpeg", nil
		}
	}
	return nil, "", fmt.Errorf("cannot compress image to fit %d byte limit", MaxPayloadBytes)
}

func EncodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	enc := &png.Encoder{CompressionLevel: png.BestCompression}
	_ = enc.Encode(&buf, img)
	return buf.Bytes()
}

func EncodeJPEG(img image.Image, quality int) []byte {
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	return buf.Bytes()
}
