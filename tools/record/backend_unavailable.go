//go:build !record_ffmpeg || !cgo || (!windows && !linux)

package record

import (
	"context"
	"fmt"
	"image"
)

type unavailableBackend struct{}

func newPlatformBackend() captureBackend { return unavailableBackend{} }

func (unavailableBackend) Resolve(context.Context, captureRequest) (resolvedTarget, error) {
	return resolvedTarget{}, fmt.Errorf("native recorder is not linked; build the full edition with CGO and the record_ffmpeg tag")
}

func (unavailableBackend) Screenshot(context.Context, resolvedTarget) (image.Image, error) {
	return nil, fmt.Errorf("native recorder is unavailable")
}

func (unavailableBackend) Record(context.Context, resolvedTarget, string, int) (mediaInfo, error) {
	return mediaInfo{}, fmt.Errorf("native recorder is unavailable")
}
