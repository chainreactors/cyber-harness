package command

type ContentBlock struct {
	Type       string `json:"type"`
	Text       string `json:"text,omitempty"`
	MimeType   string `json:"mime_type,omitempty"`
	Base64Data string `json:"base64_data,omitempty"`
}

func TextBlock(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}

func ImageBlock(mimeType, base64Data string) ContentBlock {
	return ContentBlock{Type: "image", MimeType: mimeType, Base64Data: base64Data}
}
