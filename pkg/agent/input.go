package agent

import (
	"encoding/base64"
	"fmt"
	"net/http"
	"os"
	"strings"

	"github.com/chainreactors/aiscan/pkg/aop"
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
	Parts []InputPart
	// NoEcho suppresses the user message echo event — set by boundaries
	// (web) that already delivered/persisted the message themselves.
	NoEcho bool
}

func TextInput(text string) Input {
	return Input{Parts: []InputPart{{Text: text}}}
}

// InputFromAOPMessage maps the protocol's typed message parts into the Agent's
// provider input. Session.Run is the only runtime entry point that calls it.
func InputFromAOPMessage(data aop.MessageData) Input {
	input := Input{}
	for _, part := range data.Parts {
		switch part.Type {
		case aop.PartText:
			input.Parts = append(input.Parts, InputPart{Text: part.Text})
		case aop.PartImage:
			if part.Image == nil {
				continue
			}
			input.Parts = append(input.Parts, InputPart{Image: &InputImage{
				Path:      part.Image.Path,
				Base64:    part.Image.Base64,
				MediaType: part.Image.MediaType,
			}})
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
	hasImage := false
	for _, p := range in.Parts {
		if p.Image != nil {
			hasImage = true
			break
		}
	}
	if !hasImage {
		return NewTextMessage("user", in.Text()), nil
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
	return NewMultimodalMessage("user", parts), nil
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
