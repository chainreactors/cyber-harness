package ioa

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"sync"

	"github.com/chainreactors/aiscan/pkg/command"
	acpclient "github.com/chainreactors/ioa/client"
	"github.com/chainreactors/ioa"
)

type toolBase struct {
	client   acpclient.API
	opts     acpclient.ToolOptions
	mu       sync.Mutex
}

func (b *toolBase) ensureNode(ctx context.Context) error {
	if b.client.NodeID() != "" {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.client.NodeID() != "" {
		return nil
	}
	name := b.opts.NodeName
	if name == "" {
		name = "ioa-agent"
	}
	meta := b.opts.NodeMeta
	if meta == nil {
		meta = map[string]interface{}{}
	}
	_, err := b.client.RegisterNode(ctx, name, meta)
	return err
}

func encodeResult(value interface{}) (string, error) {
	data, err := json.MarshalIndent(value, "", "  ")
	if err != nil {
		return "", err
	}
	return string(data), nil
}

func NewCommands(client acpclient.API, nodeName string, meta map[string]any) []command.PseudoCommand {
	base := &toolBase{
		client: client,
		opts:   acpclient.ToolOptions{NodeName: nodeName, NodeMeta: meta},
	}
	return []command.PseudoCommand{
		&SpaceCommand{base: base},
		&SendCommand{base: base},
		&ReadCommand{base: base},
	}
}

// SpaceCommand creates or joins an IOA collaboration space.
type SpaceCommand struct {
	base *toolBase
}

func (c *SpaceCommand) Name() string { return "ioa_space" }

func (c *SpaceCommand) Usage() string {
	return `ioa_space - Create or join an IOA message space for collaboration

Usage:
  ioa_space --name <space_name> --description <desc>

Options:
  --name         Space name (required)
  --description  Your role or intent in this space (required)`
}

func (c *SpaceCommand) Execute(ctx context.Context, args []string) (string, error) {
	fs := flag.NewFlagSet("ioa_space", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	name := fs.String("name", "", "")
	desc := fs.String("description", "", "")
	if err := fs.Parse(args); err != nil {
		return "", fmt.Errorf("ioa_space: %w\n\n%s", err, c.Usage())
	}
	if *name == "" || *desc == "" {
		return "", fmt.Errorf("ioa_space: --name and --description are required\n\n%s", c.Usage())
	}
	if err := c.base.ensureNode(ctx); err != nil {
		return "", err
	}
	info, err := c.base.client.Space(ctx, *name, *desc)
	if err != nil {
		return "", err
	}
	allMessages, err := c.base.client.Read(ctx, info.ID, ioa.ReadOptions{All: true})
	if err != nil {
		return encodeResult(info)
	}
	var startMessages []ioa.Message
	for _, m := range allMessages {
		if len(m.Refs.Messages) == 0 && len(m.Refs.Nodes) == 0 {
			startMessages = append(startMessages, m)
		}
	}
	return encodeResult(struct {
		ioa.SpaceInfo
		StartMessages []ioa.Message `json:"start_messages"`
	}{SpaceInfo: info, StartMessages: startMessages})
}

// SendCommand sends a structured message to an IOA space.
type SendCommand struct {
	base *toolBase
}

func (c *SendCommand) Name() string { return "ioa_send" }

func (c *SendCommand) Usage() string {
	return `ioa_send - Send a structured IOA message to a space

Usage:
  ioa_send --space_id <id> --content <json> [--refs <json>] [--content_schema <json>]

Options:
  --space_id        IOA space id (required)
  --content         Structured message content as JSON (required)
  --refs            Optional references JSON: {"messages":["id"],"nodes":["id"]}
  --content_schema  Optional JSON Schema to set on the space`
}

func (c *SendCommand) Execute(ctx context.Context, args []string) (string, error) {
	fs := flag.NewFlagSet("ioa_send", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	spaceID := fs.String("space_id", "", "")
	contentStr := fs.String("content", "", "")
	refsStr := fs.String("refs", "", "")
	schemaStr := fs.String("content_schema", "", "")
	if err := fs.Parse(args); err != nil {
		return "", fmt.Errorf("ioa_send: %w\n\n%s", err, c.Usage())
	}
	if *spaceID == "" || *contentStr == "" {
		return "", fmt.Errorf("ioa_send: --space_id and --content are required\n\n%s", c.Usage())
	}
	if err := c.base.ensureNode(ctx); err != nil {
		return "", err
	}

	var content map[string]interface{}
	if err := json.Unmarshal([]byte(*contentStr), &content); err != nil {
		return "", fmt.Errorf("ioa_send: invalid --content JSON: %w", err)
	}

	msg := ioa.SendMessage{Content: content}
	if *refsStr != "" {
		var refs ioa.Ref
		if err := json.Unmarshal([]byte(*refsStr), &refs); err != nil {
			return "", fmt.Errorf("ioa_send: invalid --refs JSON: %w", err)
		}
		msg.Refs = &refs
	}
	if *schemaStr != "" {
		var schema map[string]interface{}
		if err := json.Unmarshal([]byte(*schemaStr), &schema); err != nil {
			return "", fmt.Errorf("ioa_send: invalid --content_schema JSON: %w", err)
		}
		msg.ContentSchema = schema
	}

	message, err := c.base.client.Send(ctx, *spaceID, msg)
	if err != nil {
		return "", err
	}
	return encodeResult(message)
}

// ReadCommand reads messages from an IOA space.
type ReadCommand struct {
	base *toolBase
}

func (c *ReadCommand) Name() string { return "ioa_read" }

func (c *ReadCommand) Usage() string {
	return `ioa_read - Read IOA messages from a space

Usage:
  ioa_read --space_id <id> [--message_id <id>] [--after <id>] [--limit N] [--all]

Options:
  --space_id    IOA space id (required)
  --message_id  Optional message id to read its ancestor/descendant context
  --after       Optional message id cursor
  --limit       Maximum number of messages (default: 0 = no limit)
  --all         Read all messages instead of only messages addressed to this node`
}

func (c *ReadCommand) Execute(ctx context.Context, args []string) (string, error) {
	fs := flag.NewFlagSet("ioa_read", flag.ContinueOnError)
	fs.SetOutput(io.Discard)
	spaceID := fs.String("space_id", "", "")
	messageID := fs.String("message_id", "", "")
	after := fs.String("after", "", "")
	limit := fs.Int("limit", 0, "")
	all := fs.Bool("all", false, "")
	if err := fs.Parse(args); err != nil {
		return "", fmt.Errorf("ioa_read: %w\n\n%s", err, c.Usage())
	}
	if *spaceID == "" {
		return "", fmt.Errorf("ioa_read: --space_id is required\n\n%s", c.Usage())
	}
	if err := c.base.ensureNode(ctx); err != nil {
		return "", err
	}

	messages, err := c.base.client.Read(ctx, *spaceID, ioa.ReadOptions{
		MessageID: *messageID,
		After:     *after,
		Limit:     *limit,
		All:       *all,
	})
	if err != nil {
		return "", err
	}
	return encodeResult(messages)
}
