package command

type ContentBlock struct {
	Type string `json:"type"`
	Text string `json:"text,omitempty"`
}

func TextBlock(text string) ContentBlock {
	return ContentBlock{Type: "text", Text: text}
}
