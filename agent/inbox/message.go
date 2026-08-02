package inbox

import (
	"fmt"
	"strings"
	"time"

	aop "github.com/chainreactors/aiscan/aop"
)

type Origin string

const (
	OriginUser    Origin = "user"
	OriginPeer    Origin = "peer"
	OriginSession Origin = "session"
	OriginSystem  Origin = "system"
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
	Message     *aop.Message
	Origin      Origin
	Priority    Priority
	Attachments []Attachment
	Meta        map[string]any
	CreatedAt   time.Time
}

func NewMessage(origin Origin, role, content string) Message {
	return Message{
		Message:   &aop.Message{Role: role, Content: []*aop.Content{aop.Text(content)}},
		Origin:    origin,
		CreatedAt: time.Now(),
	}
}

func NewUserMessage(content string) Message {
	return NewMessage(OriginUser, "user", content)
}

func NewSystemMessage(content string) Message {
	return NewMessage(OriginSystem, "user", content)
}

func FromAOPMessage(msg *aop.Message, origin Origin) Message {
	return Message{
		Message:   msg,
		Origin:    origin,
		CreatedAt: time.Now(),
	}
}

// ToMessages converts an inbox Message to LLM-bound aop messages.
// User-origin messages with no attachments pass through unchanged.
// All other origins get a metadata envelope so the LLM knows the source.
func (m Message) ToMessages() []*aop.Message {
	if !m.needsEnvelope() && len(m.Attachments) == 0 {
		return []*aop.Message{m.Message}
	}
	rendered := m.renderContent()
	msg := m.Message
	return []*aop.Message{{Id: msg.Id, Role: msg.Role, Name: msg.Name, Content: []*aop.Content{aop.Text(rendered)}}}
}

func messageText(msg *aop.Message) string {
	if msg == nil {
		return ""
	}
	var sb strings.Builder
	for _, part := range msg.Content {
		if text := part.GetText(); text != nil {
			sb.WriteString(text.Text)
		}
	}
	return sb.String()
}

func (m Message) renderContent() string {
	body := messageText(m.Message)

	var sb strings.Builder

	if m.needsEnvelope() {
		sb.WriteString(fmt.Sprintf("<message origin=%q", m.Origin))
		if !m.CreatedAt.IsZero() {
			sb.WriteString(fmt.Sprintf(" time=%q", m.CreatedAt.Format(time.RFC3339)))
		}
		if sender, _ := m.Meta["sender"].(string); sender != "" {
			sb.WriteString(fmt.Sprintf(" sender=%q", sender))
		}
		if msgID, _ := m.Meta["message_id"].(string); msgID != "" {
			sb.WriteString(fmt.Sprintf(" message_id=%q", msgID))
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
