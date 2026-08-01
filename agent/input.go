package agent

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"

	aop "github.com/chainreactors/aiscan/aop"
)

// maxInputImageBytes caps a single input image (20 MiB), matching common
// provider limits.
const maxInputImageBytes = 20 << 20

// InputImage is a user-supplied image, either by local path (read and encoded
// by the agent) or inline base64 with an explicit media type.
type InputImage struct {
	Path      string
	Base64    string
	MediaType string
}

// InputPart is one part of a user input: text or image.
type InputPart struct {
	Text  string
	Image *InputImage
}

// Input is the agent's inbound unit. A text-only input becomes a plain user
// message; inputs with images become a multimodal message.
type Input struct {
	MessageID string
	Role      string
	Name      string
	Parts     []InputPart
}

func TextInput(text string) Input {
	return Input{Parts: []InputPart{{Text: text}}}
}

// InputFromAOPMessage maps the protocol's typed message parts into the Agent's
// provider input. Session.Run is the only runtime entry point that calls it.
func InputFromAOPMessage(message *aop.Message) Input {
	input := Input{MessageID: message.GetId(), Role: message.GetRole(), Name: message.GetName()}
	if message == nil {
		return input
	}
	for _, content := range message.Content {
		switch value := content.Value.(type) {
		case *aop.Content_Text:
			input.Parts = append(input.Parts, InputPart{Text: value.Text.Text})
		case *aop.Content_Media:
			if value.Media.Kind != "image" || value.Media.Resource == nil {
				continue
			}
			image := &InputImage{MediaType: value.Media.Resource.MediaType}
			switch source := value.Media.Resource.Source.(type) {
			case *aop.Resource_Data:
				image.Base64 = base64.StdEncoding.EncodeToString(source.Data)
			case *aop.Resource_Uri:
				image.Path = source.Uri
			}
			input.Parts = append(input.Parts, InputPart{Image: image})
		}
	}
	return input
}

// Text returns the textual parts joined in their original order. Image parts
// are intentionally omitted; callers use Parts when they need the full input.
func (in Input) Text() string {
	var sb strings.Builder
	for _, p := range in.Parts {
		if p.Text == "" {
			continue
		}
		if sb.Len() > 0 {
			sb.WriteString("\n")
		}
		sb.WriteString(p.Text)
	}
	return sb.String()
}

// chatMessage validates the input and converts it to an LLM message.
func (in Input) chatMessage() (ChatMessage, error) {
	role := in.Role
	if role == "" {
		role = "user"
	}
	hasImage := false
	for _, p := range in.Parts {
		if p.Image != nil {
			hasImage = true
			break
		}
	}
	if !hasImage {
		message := NewTextMessage(role, in.Text())
		message.AOPMessageID = in.MessageID
		message.Name = in.Name
		return message, nil
	}
	parts := make([]ContentPart, 0, len(in.Parts))
	for _, p := range in.Parts {
		if p.Text != "" {
			parts = append(parts, TextPart(p.Text))
		}
		if p.Image == nil {
			continue
		}
		mediaType, data, err := p.Image.load()
		if err != nil {
			return ChatMessage{}, err
		}
		parts = append(parts, ImagePart(mediaType, data, "high"))
	}
	message := NewMultimodalMessage(role, parts)
	message.AOPMessageID = in.MessageID
	message.Name = in.Name
	return message, nil
}

// load resolves the image to (mediaType, base64Data), enforcing the size cap.
func (im *InputImage) load() (string, string, error) {
	if im.Path != "" {
		raw, err := os.ReadFile(im.Path)
		if err != nil {
			return "", "", fmt.Errorf("read image %s: %w", im.Path, err)
		}
		if len(raw) > maxInputImageBytes {
			return "", "", fmt.Errorf("image %s exceeds %d MiB limit", im.Path, maxInputImageBytes>>20)
		}
		mediaType := im.MediaType
		if mediaType == "" {
			mediaType = http.DetectContentType(raw)
		}
		return mediaType, base64.StdEncoding.EncodeToString(raw), nil
	}
	if im.Base64 == "" {
		return "", "", fmt.Errorf("image part has neither path nor base64 data")
	}
	raw, err := base64.StdEncoding.DecodeString(im.Base64)
	if err != nil {
		return "", "", fmt.Errorf("decode image base64: %w", err)
	}
	if len(raw) > maxInputImageBytes {
		return "", "", fmt.Errorf("image exceeds %d MiB limit", maxInputImageBytes>>20)
	}
	mediaType := im.MediaType
	if mediaType == "" {
		return "", "", fmt.Errorf("base64 image requires media_type")
	}
	return mediaType, im.Base64, nil
}
