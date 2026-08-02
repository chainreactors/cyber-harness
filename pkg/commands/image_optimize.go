package commands

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
	maxDimension    = 2000
	maxPayloadBytes = 3_400_000 // raw bytes; ~4.5MB base64, below Anthropic's 5MB limit
)

var jpegQualities = []int{85, 70, 55, 40}

type optimizedImage struct {
	MimeType string
	Data     []byte
	OrigW    int
	OrigH    int
	FinalW   int
	FinalH   int
}

func optimizeImage(r io.Reader, srcMime string) (*optimizedImage, error) {
	raw, err := io.ReadAll(r)
	if err != nil {
		return nil, err
	}

	// GIF: pass through without decoding (may be animated)
	if srcMime == "image/gif" {
		return passthrough(raw, srcMime)
	}

	img, _, err := image.Decode(bytes.NewReader(raw))
	if err != nil {
		return passthrough(raw, srcMime)
	}

	bounds := img.Bounds()
	origW, origH := bounds.Dx(), bounds.Dy()

	img = resizeIfNeeded(img, origW, origH)
	finalBounds := img.Bounds()
	finalW, finalH := finalBounds.Dx(), finalBounds.Dy()

	data, mime, err := pickSmallestEncoding(img)
	if err != nil {
		return nil, err
	}

	return &optimizedImage{
		MimeType: mime,
		Data:     data,
		OrigW:    origW,
		OrigH:    origH,
		FinalW:   finalW,
		FinalH:   finalH,
	}, nil
}

func passthrough(raw []byte, mime string) (*optimizedImage, error) {
	if len(raw) > maxPayloadBytes {
		return nil, fmt.Errorf("image too large after encoding (%d bytes, max %d)", len(raw), maxPayloadBytes)
	}
	return &optimizedImage{
		MimeType: mime,
		Data:     raw,
	}, nil
}

func resizeIfNeeded(img image.Image, w, h int) image.Image {
	if w <= maxDimension && h <= maxDimension {
		return img
	}

	var newW, newH int
	if w > h {
		newW = maxDimension
		newH = h * maxDimension / w
	} else {
		newH = maxDimension
		newW = w * maxDimension / h
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

// pickSmallestEncoding tries PNG and multiple JPEG quality levels,
// returning the smallest encoding that fits under maxPayloadBytes.
func pickSmallestEncoding(img image.Image) (data []byte, mime string, err error) {
	pngData := encodePNG(img)
	jpegData := encodeJPEG(img, jpegQualities[0])

	// Pick smaller of PNG vs best-quality JPEG
	best := pngData
	bestMime := "image/png"
	if len(jpegData) < len(best) {
		best = jpegData
		bestMime = "image/jpeg"
	}

	if len(best) <= maxPayloadBytes {
		return best, bestMime, nil
	}

	// Too large — try lower JPEG qualities
	for _, q := range jpegQualities[1:] {
		jpegData = encodeJPEG(img, q)
		if len(jpegData) <= maxPayloadBytes {
			return jpegData, "image/jpeg", nil
		}
	}

	// Still too large — progressively shrink dimensions
	bounds := img.Bounds()
	w, h := bounds.Dx(), bounds.Dy()
	for w > 1 && h > 1 {
		w = w * 3 / 4
		h = h * 3 / 4
		if w < 1 {
			w = 1
		}
		if h < 1 {
			h = 1
		}
		dst := image.NewRGBA(image.Rect(0, 0, w, h))
		draw.CatmullRom.Scale(dst, dst.Bounds(), img, img.Bounds(), draw.Over, nil)
		jpegData = encodeJPEG(dst, jpegQualities[0])
		if len(jpegData) <= maxPayloadBytes {
			return jpegData, "image/jpeg", nil
		}
	}

	return nil, "", fmt.Errorf("cannot compress image to fit %d byte limit", maxPayloadBytes)
}

func encodePNG(img image.Image) []byte {
	var buf bytes.Buffer
	enc := &png.Encoder{CompressionLevel: png.BestCompression}
	_ = enc.Encode(&buf, img)
	return buf.Bytes()
}

func encodeJPEG(img image.Image, quality int) []byte {
	var buf bytes.Buffer
	_ = jpeg.Encode(&buf, img, &jpeg.Options{Quality: quality})
	return buf.Bytes()
}
