package ioa

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"sync"

	"github.com/chainreactors/aiscan/core/telemetry"
	"github.com/chainreactors/aiscan/pkg/commands"
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

func NewCommands(client protocols.ClientAPI, nodeName string, meta map[string]any) []commands.Command {
	root := &rootCommand{client: client, binding: &spaceBinding{}, nodeName: nodeName, meta: meta}
	return []commands.Command{
		{
			Name: "ioa", Usage: root.Usage(),
			DescriptionPath: "aiscan://skills/ioa/SKILL.md",
			Run:             root.Run,
			SetDefaultSpace: root.binding.set,
		},
	}
}

// rootCommand dispatches `ioa <space|send|read> ...`. send/read are delegated
// to the ioa module's client CLI (go-flags based) with the bound space
// injected; space keeps aiscan's current-space binding semantics.
type rootCommand struct {
	client   protocols.ClientAPI
	binding  *spaceBinding
	nodeName string
	meta     map[string]any
}

func (c *rootCommand) Usage() string {
	return `ioa - IOA shared-space collaboration

Subcommands:
  ioa space <name> <description> [--tag t]      Join or create a space (sets it as current)
  ioa space list|nodes|topics                   Inspect spaces and current-space members
  ioa send --content <json> [--ref-nodes ids] [--ref-messages ids] [--content-type t]
  ioa send <protocol> [flags]                   Typed protocol send (checkpoint, handoff, ...)
  ioa read [--all] [--limit N] [--after id] [--message id] [--direction d] [--listen]

The current space is injected as --space on send/read.
Reference: aiscan://skills/ioa/SKILL.md`
}

func (c *rootCommand) Run(ctx context.Context, execution *commands.Execution) (_ any, err error) {
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

// dispatchCLI runs send/read through the ioa module's client CLI with the
// current space injected as --space.
func (c *rootCommand) dispatchCLI(ctx context.Context, execution *commands.Execution, args []string) error {
	spaceID := c.binding.get()
	if spaceID == "" {
		return fmt.Errorf("no space joined. Use ioa space <name> <description> first")
	}
	if err := ensureNode(ctx, c.client, c.nodeName, c.meta); err != nil {
		return err
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
	return ioaclient.Dispatch(ctx, c.client, c.nodeName, opts, parser.Active, execution.Stdout)
}

// runSpace handles join (ioa CLI positional syntax) plus aiscan's
// binding-aware extras (list/nodes/topics).
func (c *rootCommand) runSpace(ctx context.Context, execution *commands.Execution, args []string) error {
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
	c.binding.set(info.ID)
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
