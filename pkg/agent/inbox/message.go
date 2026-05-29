package inbox

import (
	"fmt"
	"strings"
	"time"

	"github.com/chainreactors/aiscan/pkg/agent/provider"
)

type Origin string

const (
	OriginUser   Origin = "user"
	OriginPeer   Origin = "peer"
	OriginSession Origin = "session"
	OriginSystem Origin = "system"
)

type Priority int

const (
	PriorityLow    Priority = -10
	PriorityNormal Priority = 0
	PriorityHigh   Priority = 10
)

type Attachment struct {
	Type    string // "file", "skill", "raw"
	Ref     string // e.g. "@/tmp/targets.txt", "@scan"
	Content string
	Error   string
}

type Message struct {
	ChatMessage provider.ChatMessage
	Origin      Origin
	Priority    Priority
	Attachments []Attachment
	Meta        map[string]any
	CreatedAt   time.Time
}

func NewMessage(origin Origin, role, content string) Message {
	return Message{
		ChatMessage: provider.NewTextMessage(role, content),
		Origin:      origin,
		CreatedAt:   time.Now(),
	}
}

func NewUserMessage(content string) Message {
	return NewMessage(OriginUser, "user", content)
}

func NewSystemMessage(content string) Message {
	return NewMessage(OriginSystem, "user", content)
}

func FromChatMessage(msg provider.ChatMessage, origin Origin) Message {
	return Message{
		ChatMessage: msg,
		Origin:      origin,
		CreatedAt:   time.Now(),
	}
}

// ToChatMessages converts an inbox Message to LLM-compatible ChatMessages.
// User-origin messages with no attachments pass through unchanged.
// All other origins get a metadata envelope so the LLM knows the source.
func (m Message) ToChatMessages() []provider.ChatMessage {
	content := m.renderContent()
	msg := m.ChatMessage
	msg.Content = &content
	return []provider.ChatMessage{msg}
}

func (m Message) renderContent() string {
	body := ""
	if m.ChatMessage.Content != nil {
		body = *m.ChatMessage.Content
	}

	var sb strings.Builder

	if m.needsEnvelope() {
		sb.WriteString(fmt.Sprintf("<message origin=%q", m.Origin))
		if !m.CreatedAt.IsZero() {
			sb.WriteString(fmt.Sprintf(" time=%q", m.CreatedAt.Format(time.RFC3339)))
		}
		if sender, _ := m.Meta["sender"].(string); sender != "" {
			sb.WriteString(fmt.Sprintf(" sender=%q", sender))
		}
		sb.WriteString(">\n")
		sb.WriteString(body)
		renderAttachments(&sb, m.Attachments)
		sb.WriteString("\n</message>")
	} else {
		sb.WriteString(body)
		renderAttachments(&sb, m.Attachments)
	}

	return sb.String()
}

func (m Message) needsEnvelope() bool {
	return m.Origin != OriginUser && m.Origin != ""
}

func renderAttachments(sb *strings.Builder, attachments []Attachment) {
	for _, att := range attachments {
		if att.Error != "" {
			sb.WriteString(fmt.Sprintf("\n\n<attachment_error type=%q ref=%q>%s</attachment_error>", att.Type, att.Ref, att.Error))
			continue
		}
		if att.Content == "" {
			continue
		}
		sb.WriteString(fmt.Sprintf("\n\n<attachment type=%q ref=%q>\n%s\n</attachment>", att.Type, att.Ref, att.Content))
	}
}

func (m Message) WithPriority(p Priority) Message {
	m.Priority = p
	return m
}
