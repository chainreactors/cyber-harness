package ioa

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/chainreactors/cyber/core/operation"
	"io"
	"strings"
	"sync"

	"github.com/chainreactors/cyber/core/telemetry"
	coretool "github.com/chainreactors/cyber/core/tool"
	ioaclient "github.com/chainreactors/ioa/client"
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

// Serialize switching the receive subscription and publishing the command binding.
func (b *spaceBinding) change(ctx context.Context, id string, before func(context.Context, string) error) error {
	b.mu.Lock()
	defer b.mu.Unlock()
	if before != nil {
		if err := before(ctx, id); err != nil {
			return err
		}
	}
	b.spaceID = id
	return nil
}

func (b *spaceBinding) initialize(id string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if b.spaceID == "" {
		b.spaceID = id
	}
}

func NewCommands(client protocols.ClientAPI, nodeName string, meta map[string]any) []coretool.Command {
	root := &rootCommand{client: client, binding: &spaceBinding{}, nodeName: nodeName, meta: meta}
	return root.commands()
}

func (root *rootCommand) commands() []coretool.Command {
	return []coretool.Command{
		{
			Name: "ioa", Usage: root.Usage(),
			DescriptionPath: "cyber://skills/ioa/SKILL.md",
			Run:             root.Run,
		},
	}
}

// rootCommand dispatches `ioa <space|send|read> ...`. send/read are delegated
// to the ioa instance's client CLI (go-flags based) with the bound space
// injected; space keeps cyber's current-space binding semantics.
type rootCommand struct {
	beforeSpace func(context.Context, string) error
	client      protocols.ClientAPI
	binding     *spaceBinding
	nodeName    string
	meta        map[string]any
}

func (c *rootCommand) Usage() string {
	return `ioa - IOA shared-space collaboration

Subcommands:
  ioa space <name> <description> [--tag t]      Join or create a space (sets it as current)
  ioa space list|nodes|topics                   Inspect spaces and current-space members
  ioa send --content <json> [--ref-nodes ids] [--target-session id] [--ref-messages ids] [--content-type t]
  ioa send <protocol> [flags]                   Typed protocol send (checkpoint, handoff, ...)
  ioa read [--all] [--limit N] [--after id] [--message id] [--direction d] [--listen]

The current space is injected as --space on send/read.
Reference: cyber://skills/ioa/SKILL.md`
}

func (c *rootCommand) Run(ctx context.Context, execution *coretool.Execution) (_ any, err error) {
	defer telemetry.RecoverAsError("ioa", &err)
	args := execution.Args
	if len(args) == 0 {
		fmt.Fprint(execution.Stdout, c.Usage())
		return nil, nil
	}
	switch args[0] {
	case "space":
		return nil, c.runSpace(ctx, execution, args[1:])
	case "send", "read":
		return nil, c.dispatchCLI(ctx, execution, args)
	default:
		fmt.Fprint(execution.Stdout, c.Usage())
		return nil, fmt.Errorf("ioa: unknown subcommand %q", args[0])
	}
}

// dispatchCLI runs send/read through the ioa instance's client CLI with the
// current space injected as --space.
func (c *rootCommand) dispatchCLI(ctx context.Context, execution *coretool.Execution, args []string) error {
	spaceID := c.binding.get()
	if spaceID == "" {
		return fmt.Errorf("no space joined. Use ioa space <name> <description> first")
	}
	if err := ensureNode(ctx, c.client, c.nodeName, c.meta); err != nil {
		return err
	}
	target := ""
	if args[0] == "send" {
		cleaned := []string{args[0]}
		for i := 1; i < len(args); i++ {
			if args[i] == "--target-session" {
				i++
				if i >= len(args) || args[i] == "" {
					return fmt.Errorf("--target-session requires a session ID")
				}
				target = args[i]
			} else if strings.HasPrefix(args[i], "--target-session=") {
				target = strings.TrimPrefix(args[i], "--target-session=")
				if target == "" {
					return fmt.Errorf("--target-session requires a session ID")
				}
			} else {
				cleaned = append(cleaned, args[i])
			}
		}
		args = cleaned
	}
	opts := &ioaclient.CommandOptions{}
	parser := ioaclient.NewCommandParser(opts)
	full := append([]string{args[0], "--space", spaceID}, args[1:]...)
	remaining, err := parser.ParseArgs(full)
	if err != nil {
		return fmt.Errorf("ioa %s: %w", args[0], err)
	}
	if len(remaining) > 0 {
		return fmt.Errorf("ioa %s: unknown subcommand or argument %q", args[0], remaining[0])
	}
	client := c.client
	if args[0] == "send" {
		client = sessionSender{ClientAPI: c.client, source: operation.InvocationFromContext(ctx).SessionID, target: target}
	}
	return ioaclient.Dispatch(ctx, client, c.nodeName, opts, parser.Active, execution.Stdout)
}

// runSpace handles join (ioa CLI positional syntax) plus cyber's
// binding-aware extras (list/nodes/topics).
func (c *rootCommand) runSpace(ctx context.Context, execution *coretool.Execution, args []string) error {
	if len(args) > 0 {
		switch args[0] {
		case "list", "ls":
			return c.execList(ctx, execution.Stdout)
		case "nodes":
			return c.execNodes(ctx, execution.Stdout)
		case "topics":
			return c.execTopics(ctx, execution.Stdout)
		}
	}

	opts := &ioaclient.CommandOptions{}
	parser := ioaclient.NewCommandParser(opts)
	if _, err := parser.ParseArgs(append([]string{"space"}, args...)); err != nil {
		return fmt.Errorf("ioa space: %w\nusage: ioa space <name> <description> [--tag t]", err)
	}
	if err := ensureNode(ctx, c.client, c.nodeName, c.meta); err != nil {
		return err
	}
	info, startMsgs, err := ioaclient.JoinSpace(ctx, c.client, c.nodeName,
		opts.Space.Positional.Name, opts.Space.Positional.Description, opts.Space.Tags...)
	if err != nil {
		return err
	}
	if err := c.binding.change(ctx, info.ID, c.beforeSpace); err != nil {
		return err
	}
	return writeJSON(execution.Stdout, struct {
		protocols.SpaceInfo
		StartMessages []protocols.Message `json:"start_messages"`
	}{info, startMsgs})
}

func (c *rootCommand) execList(ctx context.Context, writer io.Writer) error {
	type lister interface {
		ListSpaces(ctx context.Context) ([]protocols.SpaceInfo, error)
	}
	l, ok := c.client.(lister)
	if !ok {
		return fmt.Errorf("ioa space list: not supported by this client")
	}
	spaces, err := l.ListSpaces(ctx)
	if err != nil {
		return err
	}
	return writeJSON(writer, spaces)
}

func (c *rootCommand) execNodes(ctx context.Context, writer io.Writer) error {
	spaceID := c.binding.get()
	if spaceID == "" {
		return fmt.Errorf("no space joined. Use ioa space <name> <description> first")
	}
	type infoGetter interface {
		GetSpaceInfo(ctx context.Context, spaceID string) (protocols.SpaceInfo, error)
	}
	g, ok := c.client.(infoGetter)
	if !ok {
		return fmt.Errorf("ioa space nodes: not supported by this client")
	}
	info, err := g.GetSpaceInfo(ctx, spaceID)
	if err != nil {
		return err
	}
	return writeJSON(writer, info.Nodes)
}

func (c *rootCommand) execTopics(ctx context.Context, writer io.Writer) error {
	spaceID := c.binding.get()
	if spaceID == "" {
		return fmt.Errorf("no space joined. Use ioa space <name> <description> first")
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

type autoRegisterer interface {
	EnsureRegistered(ctx context.Context, name, description string, meta map[string]any) error
}

func ensureNode(ctx context.Context, client protocols.ClientAPI, name string, meta map[string]any) error {
	if client.NodeID() != "" {
		return nil
	}
	if name == "" {
		name = "cyber-agent"
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

// sessionSender adds execution provenance to all SDK send protocols.
type sessionSender struct {
	protocols.ClientAPI
	source, target string
}

func (c sessionSender) Send(ctx context.Context, space string, body protocols.SendMessage) (protocols.Message, error) {
	meta := make(map[string]any, len(body.Meta)+2)
	for k, v := range body.Meta {
		meta[k] = v
	}
	delete(meta, "source_session_id")
	delete(meta, "target_session_id")
	if c.source != "" {
		meta["source_session_id"] = c.source
	}
	if c.target != "" {
		meta["target_session_id"] = c.target
		if body.Refs == nil {
			body.Refs = &protocols.Ref{}
		} else {
			refs := *body.Refs
			body.Refs = &refs
		}
		if len(body.Refs.Nodes) == 0 {
			body.Refs.Nodes = []string{c.NodeID()}
		}
	}
	body.Meta = meta
	return c.ClientAPI.Send(ctx, space, body)
}
