package agent

import (
	"encoding/base64"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/pkg/aop"
)

// pngBytes is a minimal PNG header so http.DetectContentType sniffs image/png.
var pngBytes = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52}

func TestTextInputProducesPlainUserMessage(t *testing.T) {
	msg, err := TextInput("hello").chatMessage()
	if err != nil {
		t.Fatal(err)
	}
	if msg.Role != "user" || msg.Content == nil || *msg.Content != "hello" {
		t.Fatalf("message = %+v", msg)
	}
	if len(msg.ContentParts) != 0 {
		t.Fatalf("text-only input must not become multimodal: %+v", msg.ContentParts)
	}
}

func TestInputImageFromPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pic.png")
	if err := os.WriteFile(path, pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	msg, err := (Input{Parts: []InputPart{
		{Text: "look"},
		{Image: &InputImage{Path: path}},
	}}).chatMessage()
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ContentParts) != 2 {
		t.Fatalf("parts = %+v, want text+image", msg.ContentParts)
	}
	if msg.ContentParts[0].Type != "text" || msg.ContentParts[0].Text != "look" {
		t.Fatalf("text part = %+v", msg.ContentParts[0])
	}
	img := msg.ContentParts[1]
	if img.Type != "image_url" || img.ImageURL == nil {
		t.Fatalf("image part = %+v", img)
	}
	mediaType, data := ParseDataURI(img.ImageURL.URL)
	if mediaType != "image/png" {
		t.Fatalf("sniffed media type = %q, want image/png", mediaType)
	}
	if data != base64.StdEncoding.EncodeToString(pngBytes) {
		t.Fatal("image base64 does not round-trip the file bytes")
	}
}

func TestInputImagePathMissing(t *testing.T) {
	_, err := (Input{Parts: []InputPart{
		{Image: &InputImage{Path: filepath.Join(t.TempDir(), "nope.png")}},
	}}).chatMessage()
	if err == nil || !strings.Contains(err.Error(), "read image") {
		t.Fatalf("err = %v", err)
	}
}

func TestInputImagePathExceedsSizeCap(t *testing.T) {
	path := filepath.Join(t.TempDir(), "huge.png")
	raw := make([]byte, maxInputImageBytes+1)
	copy(raw, pngBytes)
	if err := os.WriteFile(path, raw, 0o644); err != nil {
		t.Fatal(err)
	}
	_, err := (Input{Parts: []InputPart{{Image: &InputImage{Path: path}}}}).chatMessage()
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v", err)
	}
}

func TestInputImagePathMediaTypeOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pic.bin")
	if err := os.WriteFile(path, pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	mediaType, _, err := (&InputImage{Path: path, MediaType: "image/jpeg"}).load()
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "image/jpeg" {
		t.Fatalf("explicit media type overridden by sniffing: %q", mediaType)
	}
}

func TestInputImageBase64RequiresMediaType(t *testing.T) {
	_, _, err := (&InputImage{Base64: base64.StdEncoding.EncodeToString(pngBytes)}).load()
	if err == nil || !strings.Contains(err.Error(), "media_type") {
		t.Fatalf("err = %v", err)
	}
}

func TestInputImageBase64Invalid(t *testing.T) {
	_, _, err := (&InputImage{Base64: "!!!not-base64!!!", MediaType: "image/png"}).load()
	if err == nil || !strings.Contains(err.Error(), "base64") {
		t.Fatalf("err = %v", err)
	}
}

func TestInputImageBase64Passthrough(t *testing.T) {
	encoded := base64.StdEncoding.EncodeToString(pngBytes)
	mediaType, data, err := (&InputImage{Base64: encoded, MediaType: "image/png"}).load()
	if err != nil {
		t.Fatal(err)
	}
	if mediaType != "image/png" || data != encoded {
		t.Fatalf("load = %q, %q", mediaType, data)
	}
}

func TestInputImageEmptySource(t *testing.T) {
	_, _, err := (&InputImage{}).load()
	if err == nil || !strings.Contains(err.Error(), "neither path nor base64") {
		t.Fatalf("err = %v", err)
	}
}

func TestInputFromAOPMessageMapsParts(t *testing.T) {
	input := InputFromAOPMessage(aop.MessageData{
		MessageID: "m-1",
		Role:      "user",
		Parts: []aop.MessagePart{
			{Type: aop.PartText, Text: "hi"},
			{Type: aop.PartImage, Image: &aop.ImageSource{Base64: "AAAA", MediaType: "image/png"}},
		},
	})
	if len(input.Parts) != 2 || input.Parts[0].Text != "hi" || input.Parts[1].Image == nil {
		t.Fatalf("input = %+v", input)
	}
	if input.Parts[1].Image.Base64 != "AAAA" || input.Parts[1].Image.MediaType != "image/png" {
		t.Fatalf("image = %+v", input.Parts[1].Image)
	}
}
