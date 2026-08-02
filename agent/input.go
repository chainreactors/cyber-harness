package agent

import (
	"fmt"
	"net/http"
	"os"

	aop "github.com/chainreactors/aiscan/aop"
)

// maxInputImageBytes caps a single input image (20 MiB), matching common
// provider limits.
const maxInputImageBytes = 20 << 20

// TextInput builds a plain user message from text.
func TextInput(text string) *aop.Message {
	return &aop.Message{Role: "user", Content: []*aop.Content{aop.Text(text)}}
}

// resolveInputMessage prepares a user-supplied aop message for the provider:
// image parts referenced by file URI are read from disk and inlined as data,
// enforcing the size cap.
func resolveInputMessage(message *aop.Message) (*aop.Message, error) {
	if message == nil {
		return nil, fmt.Errorf("input message is required")
	}
	resolved := *message
	resolved.Content = make([]*aop.Content, 0, len(message.Content))
	for _, content := range message.Content {
		media := content.GetMedia()
		if media == nil || media.Kind != "image" || media.Resource == nil {
			resolved.Content = append(resolved.Content, content)
			continue
		}
		resource := media.Resource
		if data := resource.GetData(); len(data) > 0 {
			if len(data) > maxInputImageBytes {
				return nil, fmt.Errorf("image exceeds %d MiB limit", maxInputImageBytes>>20)
			}
			if resource.MediaType == "" {
				return nil, fmt.Errorf("base64 image requires media_type")
			}
			resolved.Content = append(resolved.Content, content)
			continue
		}
		uri := resource.GetUri()
		if uri == "" {
			return nil, fmt.Errorf("image part has neither data nor uri")
		}
		raw, err := os.ReadFile(uri)
		if err != nil {
			return nil, fmt.Errorf("read image %s: %w", uri, err)
		}
		if len(raw) > maxInputImageBytes {
			return nil, fmt.Errorf("image %s exceeds %d MiB limit", uri, maxInputImageBytes>>20)
		}
		mediaType := resource.MediaType
		if mediaType == "" {
			mediaType = http.DetectContentType(raw)
		}
		resolved.Content = append(resolved.Content, &aop.Content{Value: &aop.Content_Media{Media: &aop.MediaContent{
			Kind: "image",
			Resource: &aop.Resource{
				Source:    &aop.Resource_Data{Data: raw},
				MediaType: mediaType,
			},
		}}})
	}
	return &resolved, nil
}
