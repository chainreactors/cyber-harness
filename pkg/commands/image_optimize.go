package commands

import (
	"image"
	"io"

	"github.com/chainreactors/aiscan/pkg/imageutil"
)

const (
	maxDimension    = imageutil.MaxDimension
	maxPayloadBytes = imageutil.MaxPayloadBytes
)

type optimizedImage = imageutil.Optimized

func optimizeImage(r io.Reader, srcMime string) (*optimizedImage, error) {
	return imageutil.Optimize(r, srcMime)
}

func resizeIfNeeded(img image.Image, w, h int) image.Image {
	return imageutil.ResizeIfNeeded(img, w, h)
}

func encodePNG(img image.Image) []byte {
	return imageutil.EncodePNG(img)
}

func encodeJPEG(img image.Image, quality int) []byte {
	return imageutil.EncodeJPEG(img, quality)
}
