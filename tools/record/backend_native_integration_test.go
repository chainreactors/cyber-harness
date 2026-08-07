//go:build record_ffmpeg && record_integration && cgo && (windows || linux)

package record

import (
	"bytes"
	"context"
	"encoding/binary"
	"os"
	"path/filepath"
	"testing"
	"time"
)

func TestNativeDesktopScreenshotAndRecord(t *testing.T) {
	backend := newPlatformBackend()
	target, err := backend.Resolve(context.Background(), captureRequest{Target: "desktop", FPS: 10})
	if err != nil {
		t.Fatal(err)
	}
	img, err := backend.Screenshot(context.Background(), target)
	if err != nil {
		t.Fatal(err)
	}
	if img.Bounds().Dx() <= 0 || img.Bounds().Dy() <= 0 {
		t.Fatalf("invalid screenshot bounds %v", img.Bounds())
	}

	output := filepath.Join(t.TempDir(), "desktop.mp4")
	ctx, cancel := context.WithTimeout(context.Background(), 1500*time.Millisecond)
	defer cancel()
	media, err := backend.Record(ctx, target, output, 10)
	if err != nil {
		t.Fatal(err)
	}
	if media.Frames == 0 {
		t.Fatal("recording produced no frames")
	}
	if info, err := os.Stat(output); err != nil || info.Size() == 0 {
		t.Fatalf("recording output info=%v err=%v", info, err)
	}

	assertMP4H264(t, output)
}

func assertMP4H264(t *testing.T, path string) {
	t.Helper()
	data, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	topLevel := make(map[string]bool)
	for offset := 0; offset < len(data); {
		if len(data)-offset < 8 {
			t.Fatalf("truncated MP4 atom header at byte %d", offset)
		}
		size := uint64(binary.BigEndian.Uint32(data[offset : offset+4]))
		headerSize := uint64(8)
		if size == 1 {
			if len(data)-offset < 16 {
				t.Fatalf("truncated extended MP4 atom header at byte %d", offset)
			}
			size = binary.BigEndian.Uint64(data[offset+8 : offset+16])
			headerSize = 16
		} else if size == 0 {
			size = uint64(len(data) - offset)
		}
		if size < headerSize || size > uint64(len(data)-offset) {
			t.Fatalf("invalid MP4 atom size %d at byte %d", size, offset)
		}
		topLevel[string(data[offset+4:offset+8])] = true
		offset += int(size)
	}
	for _, atom := range []string{"ftyp", "moov", "mdat"} {
		if !topLevel[atom] {
			t.Fatalf("recorded MP4 is missing %s atom", atom)
		}
	}
	if !bytes.Contains(data, []byte("avc1")) {
		t.Fatal("recorded MP4 has no H.264 avc1 sample entry")
	}
}
