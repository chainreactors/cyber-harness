package tool

import "strings"

// ContentBlock represents one piece of a tool result (text or image).
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

// Result is the value returned by Tool.Execute.
type Result struct {
	Content   []ContentBlock
	IsError   bool
	Terminate bool
}

func (r Result) Text() string {
	var sb strings.Builder
	for _, block := range r.Content {
		if block.Type == "text" {
			sb.WriteString(block.Text)
		}
	}
	return sb.String()
}

func (r Result) HasImages() bool {
	for _, block := range r.Content {
		if block.Type == "image" {
			return true
		}
	}
	return false
}

func TextResult(s string) Result {
	return Result{Content: []ContentBlock{TextBlock(s)}}
}

func ErrorResult(msg string) Result {
	return Result{Content: []ContentBlock{TextBlock(msg)}, IsError: true}
}

func TerminateResult(s string) Result {
	return Result{Content: []ContentBlock{TextBlock(s)}, Terminate: true}
}
