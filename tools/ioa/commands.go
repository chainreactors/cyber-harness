package ioa

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/telemetry"
	"github.com/chainreactors/ioa/protocols"
)

// spaceBinding holds the current space ID shared across all IOA commands.
type spaceBinding struct {
	mu      sync.RWMutex
	spaceID string
}

func (b *spaceBinding) get() string {
	b.mu.RLock()
	defer b.mu.RUnlock()
	return b.spaceID
}

func (b *spaceBinding) set(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.spaceID = id
}

func NewCommands(client protocols.ClientAPI, nodeName string, meta map[string]any) []commands.Command {
	binding := &spaceBinding{}
	space := &spaceCommand{client: client, binding: binding, nodeName: nodeName, meta: meta}
	send := &sendCommand{client: client, binding: binding}
	read := &readCommand{client: client, binding: binding}
	return []commands.Command{
		{Name: space.Name(), Usage: space.Usage(), Run: space.Run, SetDefaultSpace: space.SetDefaultSpace},
		{Name: send.Name(), Usage: send.Usage(), Run: send.Run},
		{Name: read.Name(), Usage: read.Usage(), Run: read.Run},
	}
}

type autoRegisterer interface {
	EnsureRegistered(ctx context.Context, name, description string, meta map[string]any) error
}

func ensureNode(ctx context.Context, client protocols.ClientAPI, name string, meta map[string]any) error {
	if client.NodeID() != "" {
		return nil
	}
	if name == "" {
		name = "aiscan-agent"
	}
	if meta == nil {
		meta = map[string]any{}
	}
	if ar, ok := client.(autoRegisterer); ok {
		return ar.EnsureRegistered(ctx, name, "", meta)
	}
	_, err := client.RegisterNode(ctx, name, "", meta)
	return err
}

func writeJSON(writer io.Writer, v any) error {
	data, err := json.MarshalIndent(v, "", "  ")
	if err != nil {
		return err
	}
	_, err = writer.Write(data)
	return err
}

// --- ioa_space ---

type spaceCommand struct {
	client   protocols.ClientAPI
	binding  *spaceBinding
	nodeName string
	meta     map[string]any
}

func (c *spaceCommand) SetDefaultSpace(id string) { c.binding.set(id) }
func (c *spaceCommand) Name() string              { return "ioa_space" }

func (c *spaceCommand) Usage() string {
	return `ioa_space - Manage IOA spaces

Subcommands:
  ioa_space join --name <name> --description <role> [--tags a,b]
  ioa_space list
  ioa_space nodes
  ioa_space topics

join      Join or create a space (sets it as current for ioa_send/ioa_read)
list      List all available spaces on the server
nodes     Show nodes in the current space
topics    Show root messages (conversation starters) in the current space`
}

func (c *spaceCommand) Run(ctx context.Context, execution *commands.Execution) (_ any, err error) {
	defer telemetry.RecoverAsError("ioa_space", &err)
	args := execution.Args
	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "--") {
		sub = args[0]
		args = args[1:]
	}

	switch sub {
	case "join", "":
		m, err := argsToMap(args)
		if err != nil {
			return nil, fmt.Errorf("ioa_space: %w\n\n%s", err, c.Usage())
		}
		return nil, c.execJoin(ctx, execution.Stdout, m)
	case "list", "ls":
		return nil, c.execList(ctx, execution.Stdout)
	case "nodes":
		return nil, c.execNodes(ctx, execution.Stdout)
	case "topics":
		return nil, c.execTopics(ctx, execution.Stdout)
	default:
		return nil, fmt.Errorf("ioa_space: unknown subcommand %q\n\n%s", sub, c.Usage())
	}
}

func (c *spaceCommand) execJoin(ctx context.Context, writer io.Writer, m map[string]interface{}) error {
	name, _ := m["name"].(string)
	desc, _ := m["description"].(string)
	if name == "" || desc == "" {
		return fmt.Errorf("ioa_space: --name and --description are required\n\n%s", c.Usage())
	}
	var tags []string
	if raw, ok := m["tags"].(string); ok && raw != "" {
		for _, t := range strings.Split(raw, ",") {
			if t = strings.TrimSpace(t); t != "" {
				tags = append(tags, t)
			}
		}
	}

	if err := ensureNode(ctx, c.client, c.nodeName, c.meta); err != nil {
		return err
	}
	info, err := c.client.Space(ctx, name, desc, tags...)
	if err != nil {
		return err
	}
	c.binding.set(info.ID)

	allMessages, readErr := c.client.Read(ctx, info.ID, protocols.ReadOptions{All: true})
	if readErr != nil {
		return writeJSON(writer, info)
	}
	var startMessages []protocols.Message
	for _, msg := range allMessages {
		if len(msg.Refs.Messages) == 0 && len(msg.Refs.Nodes) == 0 {
			startMessages = append(startMessages, msg)
		}
	}
	return writeJSON(writer, struct {
		protocols.SpaceInfo
		StartMessages []protocols.Message `json:"start_messages"`
	}{info, startMessages})
}

func (c *spaceCommand) execList(ctx context.Context, writer io.Writer) error {
	type lister interface {
		ListSpaces(ctx context.Context) ([]protocols.SpaceInfo, error)
	}
	l, ok := c.client.(lister)
	if !ok {
		return fmt.Errorf("ioa_space list: not supported by this client")
	}
	spaces, err := l.ListSpaces(ctx)
	if err != nil {
		return err
	}
	return writeJSON(writer, spaces)
}

func (c *spaceCommand) execNodes(ctx context.Context, writer io.Writer) error {
	spaceID := c.binding.get()
	if spaceID == "" {
		return fmt.Errorf("no space joined. Use ioa_space join --name <name> --description <role> first")
	}
	type infoGetter interface {
		GetSpaceInfo(ctx context.Context, spaceID string) (protocols.SpaceInfo, error)
	}
	g, ok := c.client.(infoGetter)
	if !ok {
		return fmt.Errorf("ioa_space --nodes: not supported by this client")
	}
	info, err := g.GetSpaceInfo(ctx, spaceID)
	if err != nil {
		return err
	}
	return writeJSON(writer, info.Nodes)
}

func (c *spaceCommand) execTopics(ctx context.Context, writer io.Writer) error {
	spaceID := c.binding.get()
	if spaceID == "" {
		return fmt.Errorf("no space joined. Use ioa_space join --name <name> --description <role> first")
	}
	if err := ensureNode(ctx, c.client, c.nodeName, c.meta); err != nil {
		return err
	}
	messages, err := c.client.Read(ctx, spaceID, protocols.ReadOptions{All: true})
	if err != nil {
		return err
	}
	var topics []protocols.Message
	for _, msg := range messages {
		if len(msg.Refs.Messages) == 0 && len(msg.Refs.Nodes) == 0 {
			topics = append(topics, msg)
		}
	}
	return writeJSON(writer, topics)
}

// --- ioa_send ---

type sendCommand struct {
	client  protocols.ClientAPI
	binding *spaceBinding
}

func (c *sendCommand) Name() string { return "ioa_send" }

func (c *sendCommand) Usage() string {
	return `ioa_send - Send a message to the current IOA space

Subcommands:
  ioa_send --content '{"content": "msg"}'                        Send to space (broadcast)
  ioa_send to --node <node_id> --content '{"content": "msg"}'    Send to a specific node
  ioa_send reply --to <message_id> --content '{"content": "re"}' Reply to a message
  ioa_send checkpoint --kind <kind> --title <title> --content <body> [--target <url>] [--status <status>]

Options:
  --content         Structured message content as JSON object (required, except for checkpoint)
  --node            Target node ID (for "to" subcommand)
  --to              Message ID to reply to (for "reply" subcommand)
  --refs            Raw references JSON: '{"messages": ["id"], "nodes": ["id"]}'
  --kind            Checkpoint kind: verify, sniper, deep
  --title           Short checkpoint title
  --target          Target host:port or URL (checkpoint)
  --status          Verification status: confirmed, not_confirmed, info, inconclusive (checkpoint)`
}

func (c *sendCommand) Run(ctx context.Context, execution *commands.Execution) (_ any, err error) {
	defer telemetry.RecoverAsError("ioa_send", &err)
	args := execution.Args
	spaceID := c.binding.get()
	if spaceID == "" {
		return nil, fmt.Errorf("no space joined. Use ioa_space join first")
	}

	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "--") {
		sub = args[0]
		args = args[1:]
	}

	m, err := argsToMap(args)
	if err != nil {
		return nil, fmt.Errorf("ioa_send: %w\n\n%s", err, c.Usage())
	}

	if h := protocols.SendHandler(sub); h != nil {
		if err := ensureNode(ctx, c.client, "", nil); err != nil {
			return nil, err
		}
		env := &protocols.Env{Client: c.client, SpaceID: spaceID}
		result, err := h(ctx, env, m)
		if err != nil {
			return nil, err
		}
		fmt.Fprint(execution.Stdout, result)
		return nil, nil
	}

	content, _ := m["content"].(map[string]interface{})
	if content == nil {
		return nil, fmt.Errorf("ioa_send: --content is required and must be a JSON object\n\n%s", c.Usage())
	}

	contentType, _ := m["content_type"].(string)
	body := protocols.SendMessage{ContentType: contentType, Content: content}

	switch sub {
	case "to":
		node, _ := m["node"].(string)
		if node == "" {
			return nil, fmt.Errorf("ioa_send to: --node <node_id> is required")
		}
		body.Refs = &protocols.Ref{Nodes: []string{node}}
	case "reply":
		to, _ := m["to"].(string)
		if to == "" {
			return nil, fmt.Errorf("ioa_send reply: --to <message_id> is required")
		}
		body.Refs = &protocols.Ref{Messages: []string{to}}
	case "broadcast", "":
		if refs, ok := m["refs"].(map[string]interface{}); ok {
			data, _ := json.Marshal(refs)
			var r protocols.Ref
			if json.Unmarshal(data, &r) == nil {
				body.Refs = &r
			}
		}
	default:
		if sub != "" {
			return nil, fmt.Errorf("ioa_send: unknown subcommand %q\n\n%s", sub, c.Usage())
		}
	}

	if err := ensureNode(ctx, c.client, "", nil); err != nil {
		return nil, err
	}
	msg, err := c.client.Send(ctx, spaceID, body)
	if err != nil {
		return nil, err
	}
	return nil, writeJSON(execution.Stdout, msg)
}

// --- ioa_read ---

type readCommand struct {
	client  protocols.ClientAPI
	binding *spaceBinding
}

func (c *readCommand) Name() string { return "ioa_read" }

func (c *readCommand) Usage() string {
	return `ioa_read - Read messages from the current IOA space

Subcommands:
  ioa_read                           Read messages addressed to this node
  ioa_read all [--limit 50]          Read all messages in the space
  ioa_read thread --id <message_id>  Read context of a specific message
  ioa_read new [--after <msg_id>]    Read messages after a cursor (pagination)

Options:
  --limit           Maximum number of messages
  --after           Message ID cursor for pagination
  --id              Message ID for thread context`
}

func (c *readCommand) Run(ctx context.Context, execution *commands.Execution) (_ any, err error) {
	defer telemetry.RecoverAsError("ioa_read", &err)
	args := execution.Args
	spaceID := c.binding.get()
	if spaceID == "" {
		return nil, fmt.Errorf("no space joined. Use ioa_space join first")
	}

	sub := ""
	if len(args) > 0 && !strings.HasPrefix(args[0], "--") {
		sub = args[0]
		args = args[1:]
	}

	m, err := argsToMap(args)
	if err != nil {
		return nil, fmt.Errorf("ioa_read: %w\n\n%s", err, c.Usage())
	}

	opts := protocols.ReadOptions{}
	if v, ok := m["limit"].(int); ok {
		opts.Limit = v
	}
	if v, ok := m["after"].(string); ok {
		opts.After = v
	}

	switch sub {
	case "all":
		opts.All = true
	case "thread":
		id, _ := m["id"].(string)
		if id == "" {
			return nil, fmt.Errorf("ioa_read thread: --id <message_id> is required")
		}
		opts.MessageID = id
	case "new":
		// uses --after from flags above
	case "":
		// default: read messages addressed to this node
	default:
		return nil, fmt.Errorf("ioa_read: unknown subcommand %q\n\n%s", sub, c.Usage())
	}

	if err := ensureNode(ctx, c.client, "", nil); err != nil {
		return nil, err
	}
	messages, err := c.client.Read(ctx, spaceID, opts)
	if err != nil {
		return nil, err
	}
	return nil, writeJSON(execution.Stdout, messages)
}

// --- arg parsing ---

func argsToMap(args []string) (map[string]interface{}, error) {
	m := make(map[string]interface{})
	for i := 0; i < len(args); i++ {
		arg := args[i]
		if !strings.HasPrefix(arg, "--") {
			continue
		}
		key := strings.TrimPrefix(arg, "--")
		if i+1 >= len(args) || strings.HasPrefix(args[i+1], "--") {
			m[key] = true
			continue
		}
		i++
		val := args[i]
		if val == "true" {
			m[key] = true
		} else if val == "false" {
			m[key] = false
		} else if n, err := strconv.Atoi(val); err == nil {
			m[key] = n
		} else if json.Valid([]byte(val)) && len(val) > 0 && (val[0] == '{' || val[0] == '[') {
			var v interface{}
			if err := json.Unmarshal([]byte(val), &v); err != nil {
				return nil, fmt.Errorf("parse %s JSON: %w", key, err)
			}
			m[key] = v
		} else {
			m[key] = val
		}
	}
	return m, nil
}
