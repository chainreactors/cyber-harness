package inbox

import (
	"fmt"
	"os"
	"regexp"
	"strings"
)

const defaultMaxFileSize = 50 * 1024

type Expander struct {
	ReadFile    func(path string) (string, error)
	LookupSkill func(name string) (string, bool)
	MaxFileSize int
}

var atPattern = regexp.MustCompile(`@(?:(file|skill):)?(\S+)`)

func (e *Expander) Expand(msg Message) Message {
	if msg.ChatMessage.Content == nil {
		return msg
	}
	content := *msg.ChatMessage.Content
	matches := atPattern.FindAllStringSubmatchIndex(content, -1)
	if len(matches) == 0 {
		return msg
	}

	maxSize := e.MaxFileSize
	if maxSize <= 0 {
		maxSize = defaultMaxFileSize
	}

	var attachments []Attachment
	for _, loc := range matches {
		fullMatch := content[loc[0]:loc[1]]
		kind := ""
		if loc[2] >= 0 {
			kind = content[loc[2]:loc[3]]
		}
		ref := content[loc[4]:loc[5]]

		if kind == "" {
			kind = e.inferKind(ref)
		}

		att := Attachment{Type: kind, Ref: fullMatch}
		switch kind {
		case "file":
			att = e.expandFile(att, ref, maxSize)
		case "skill":
			att = e.expandSkill(att, ref)
		default:
			continue
		}
		attachments = append(attachments, att)
	}

	if len(attachments) == 0 {
		return msg
	}
	msg.Attachments = append(msg.Attachments, attachments...)
	return msg
}

func (e *Expander) inferKind(ref string) string {
	if strings.HasPrefix(ref, "/") || strings.HasPrefix(ref, "./") || strings.HasPrefix(ref, "~/") {
		return "file"
	}
	if e.LookupSkill != nil {
		if _, ok := e.LookupSkill(ref); ok {
			return "skill"
		}
	}
	return ""
}

func (e *Expander) expandFile(att Attachment, path string, maxSize int) Attachment {
	reader := e.ReadFile
	if reader == nil {
		reader = func(p string) (string, error) {
			data, err := os.ReadFile(p)
			return string(data), err
		}
	}
	data, err := reader(path)
	if err != nil {
		att.Error = fmt.Sprintf("read %s: %s", path, err)
		return att
	}
	if len(data) > maxSize {
		att.Content = data[:maxSize] + fmt.Sprintf("\n...[truncated: %d/%d bytes]", maxSize, len(data))
	} else {
		att.Content = data
	}
	return att
}

func (e *Expander) expandSkill(att Attachment, name string) Attachment {
	if e.LookupSkill == nil {
		att.Error = "skill lookup not configured"
		return att
	}
	body, ok := e.LookupSkill(name)
	if !ok {
		att.Error = fmt.Sprintf("skill %q not found", name)
		return att
	}
	att.Content = body
	return att
}
