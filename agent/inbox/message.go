package inbox

import (
	"fmt"
	"strings"
	"time"

	aop "github.com/chainreactors/cyber/aop"
	"github.com/chainreactors/cyber/core/collaboration"
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
	Ref     string // e.g. "@/tmp/input.txt", "@artifact"
	Content string
	Error   string
}

// Metadata is the fixed routing and provenance envelope for an inbox message.
type Metadata struct {
	Sender          string                  `json:"sender,omitempty"`
	MessageID       string                  `json:"message_id,omitempty"`
	SourceSessionID string                  `json:"source_session_id,omitempty"`
	TargetSessionID string                  `json:"target_session_id,omitempty"`
	Space           string                  `json:"space,omitempty"`
	TaskID          string                  `json:"task_id,omitempty"`
	Kind            string                  `json:"kind,omitempty"`
	FeedbackID      string                  `json:"feedback_id,omitempty"`
	FeedbackChain   []string                `json:"feedback_chain,omitempty"`
	SourceTaskID    string                  `json:"source_task_id,omitempty"`
	ExecutionID     string                  `json:"execution_id,omitempty"`
	Status          string                  `json:"status,omitempty"`
	ReplyNode       string                  `json:"reply_node,omitempty"`
	ReplySession    string                  `json:"reply_session,omitempty"`
	ReplyMessage    string                  `json:"reply_message,omitempty"`
	Record          *collaboration.Envelope `json:"record,omitempty"`
}

func (m Metadata) Clone() Metadata {
	m.FeedbackChain = append([]string(nil), m.FeedbackChain...)
	if m.Record != nil {
		record := *m.Record
		record.Chain = append([]string(nil), record.Chain...)
		if record.Agent != nil {
			agent := *record.Agent
			record.Agent = &agent
		}
		m.Record = &record
	}
	return m
}

type Message struct {
	Message     *aop.Message
	Origin      Origin
	Priority    Priority
	Interrupt   bool // Request the current consumer to yield after this message is admitted.
	Attachments []Attachment
	Meta        Metadata
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
		if m.Meta.Sender != "" {
			sb.WriteString(fmt.Sprintf(" sender=%q", m.Meta.Sender))
		}
		msgID := m.Meta.MessageID
		if msgID == "" && m.Message != nil {
			msgID = m.Message.Id
		}
		if msgID != "" {
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
