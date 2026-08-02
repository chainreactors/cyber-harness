package agent

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/chainreactors/aiscan/agent/provider"
	aop "github.com/chainreactors/aiscan/aop"
)

// pngBytes is a minimal PNG header so http.DetectContentType sniffs image/png.
var pngBytes = []byte{0x89, 0x50, 0x4E, 0x47, 0x0D, 0x0A, 0x1A, 0x0A, 0x00, 0x00, 0x00, 0x0D, 0x49, 0x48, 0x44, 0x52}

func uriImageMessage(uri, mediaType string) *aop.Message {
	return &aop.Message{Role: "user", Content: []*aop.Content{{Value: &aop.Content_Media{Media: &aop.MediaContent{
		Kind:     "image",
		Resource: &aop.Resource{Source: &aop.Resource_Uri{Uri: uri}, MediaType: mediaType},
	}}}}}
}

func dataImageMessage(data []byte, mediaType string) *aop.Message {
	return &aop.Message{Role: "user", Content: []*aop.Content{{Value: &aop.Content_Media{Media: &aop.MediaContent{
		Kind:     "image",
		Resource: &aop.Resource{Source: &aop.Resource_Data{Data: data}, MediaType: mediaType},
	}}}}}
}

func TestTextInputProducesPlainUserMessage(t *testing.T) {
	msg := TextInput("hello")
	if msg.Role != "user" || provider.MessageText(msg) != "hello" {
		t.Fatalf("message = %+v", msg)
	}
	for _, part := range msg.Content {
		if part.GetMedia() != nil {
			t.Fatalf("text-only input must not become multimodal: %+v", msg.Content)
		}
	}
}

func TestInputImageFromPath(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pic.png")
	if err := os.WriteFile(path, pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}

	msg, err := resolveInputMessage(&aop.Message{Role: "user", Content: []*aop.Content{
		aop.Text("look"),
		uriImageMessage(path, "").Content[0],
	}})
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.Content) != 2 {
		t.Fatalf("parts = %+v, want text+image", msg.Content)
	}
	if msg.Content[0].GetText().GetText() != "look" {
		t.Fatalf("text part = %+v", msg.Content[0])
	}
	media := msg.Content[1].GetMedia()
	if media == nil || media.Resource == nil {
		t.Fatalf("image part = %+v", msg.Content[1])
	}
	if media.Resource.MediaType != "image/png" {
		t.Fatalf("sniffed media type = %q, want image/png", media.Resource.MediaType)
	}
	if string(media.Resource.GetData()) != string(pngBytes) {
		t.Fatal("image data does not round-trip the file bytes")
	}
}

func TestInputImagePathMissing(t *testing.T) {
	_, err := resolveInputMessage(uriImageMessage(filepath.Join(t.TempDir(), "nope.png"), ""))
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
	_, err := resolveInputMessage(uriImageMessage(path, ""))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v", err)
	}
}

func TestInputImagePathMediaTypeOverride(t *testing.T) {
	path := filepath.Join(t.TempDir(), "pic.bin")
	if err := os.WriteFile(path, pngBytes, 0o644); err != nil {
		t.Fatal(err)
	}
	msg, err := resolveInputMessage(uriImageMessage(path, "image/jpeg"))
	if err != nil {
		t.Fatal(err)
	}
	if got := msg.Content[0].GetMedia().Resource.MediaType; got != "image/jpeg" {
		t.Fatalf("explicit media type overridden by sniffing: %q", got)
	}
}

func TestInputImageDataRequiresMediaType(t *testing.T) {
	_, err := resolveInputMessage(dataImageMessage(pngBytes, ""))
	if err == nil || !strings.Contains(err.Error(), "media_type") {
		t.Fatalf("err = %v", err)
	}
}

func TestInputImageDataExceedsSizeCap(t *testing.T) {
	raw := make([]byte, maxInputImageBytes+1)
	copy(raw, pngBytes)
	_, err := resolveInputMessage(dataImageMessage(raw, "image/png"))
	if err == nil || !strings.Contains(err.Error(), "exceeds") {
		t.Fatalf("err = %v", err)
	}
}

func TestInputImageDataPassthrough(t *testing.T) {
	msg, err := resolveInputMessage(dataImageMessage(pngBytes, "image/png"))
	if err != nil {
		t.Fatal(err)
	}
	media := msg.Content[0].GetMedia()
	if media.Resource.MediaType != "image/png" || string(media.Resource.GetData()) != string(pngBytes) {
		t.Fatalf("load = %q, %d bytes", media.Resource.MediaType, len(media.Resource.GetData()))
	}
}

func TestInputImageEmptySource(t *testing.T) {
	_, err := resolveInputMessage(&aop.Message{Role: "user", Content: []*aop.Content{{Value: &aop.Content_Media{Media: &aop.MediaContent{
		Kind:     "image",
		Resource: &aop.Resource{},
	}}}}})
	if err == nil || !strings.Contains(err.Error(), "neither data nor uri") {
		t.Fatalf("err = %v", err)
	}
}

func TestResolveInputMessageKeepsTextAndInlineImages(t *testing.T) {
	msg, err := resolveInputMessage(&aop.Message{
		Id:   "m-1",
		Role: "user",
		Content: []*aop.Content{
			aop.Text("hi"),
			aop.Image("image/png", []byte{0, 0, 0}),
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	if msg.Id != "m-1" || len(msg.Content) != 2 {
		t.Fatalf("message = %+v", msg)
	}
	if provider.MessageText(msg) != "hi" || msg.Content[1].GetMedia() == nil {
		t.Fatalf("message = %+v", msg)
	}
}
