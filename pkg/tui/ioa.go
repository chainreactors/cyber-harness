package tui

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"text/tabwriter"

	cfg "github.com/chainreactors/aiscan/core/config"
	ioaclient "github.com/chainreactors/ioa/client"
	"github.com/chainreactors/ioa/protocols"
)

func RunIOASpaces(ctx context.Context, client *ioaclient.Client, option *cfg.Option, stdout, stderr io.Writer) error {
	spaces, err := client.ListSpaces(ctx)
	if err != nil {
		return err
	}
	if option.IOAJSON {
		return writeJSONOutput(stdout, spaces)
	}
	if len(spaces) == 0 {
		fmt.Fprintln(stderr, "no spaces found")
		return nil
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "ID\tNAME\tNODES\tMESSAGES\n")
	for _, s := range spaces {
		fmt.Fprintf(w, "%s\t%s\t%d\t%d\n", s.ID, s.Name, len(s.Nodes), s.MessageCount)
	}
	return w.Flush()
}

func RunIOAMessages(ctx context.Context, client *ioaclient.Client, option *cfg.Option, args cfg.IOAClientArgs, stdout, stderr io.Writer) error {
	space, err := client.ResolveSpace(ctx, args.Space)
	if err != nil {
		return err
	}
	messages, err := client.ReadPublic(ctx, space.ID, protocols.ReadOptions{})
	if err != nil {
		return err
	}
	if option.IOAJSON {
		return writeJSONOutput(stdout, messages)
	}
	if len(messages) == 0 {
		fmt.Fprintf(stderr, "no start messages in space %q\n", space.Name)
		return nil
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "ID\tSENDER\tCONTENT\n")
	for _, m := range messages {
		fmt.Fprintf(w, "%s\t%s\t%s\n", m.ID, m.Sender, contentPreview(m.Content, 80))
	}
	return w.Flush()
}

func RunIOAContext(ctx context.Context, client *ioaclient.Client, option *cfg.Option, args cfg.IOAClientArgs, stdout, stderr io.Writer) error {
	space, err := client.ResolveSpace(ctx, args.Space)
	if err != nil {
		return err
	}
	messages, err := client.ReadPublic(ctx, space.ID, protocols.ReadOptions{MessageID: args.MessageID})
	if err != nil {
		return err
	}
	if option.IOAJSON {
		return writeJSONOutput(stdout, messages)
	}
	if len(messages) == 0 {
		fmt.Fprintf(stderr, "no messages in context of %s\n", args.MessageID)
		return nil
	}
	for _, m := range messages {
		marker := " "
		if m.ID == args.MessageID {
			marker = "*"
		}
		fmt.Fprintf(stdout, "%s [%s] %s:\n  %s\n", marker, m.ID, m.Sender, contentPreview(m.Content, 120))
	}
	return nil
}

func RunIOANodes(ctx context.Context, client *ioaclient.Client, option *cfg.Option, args cfg.IOAClientArgs, stdout, stderr io.Writer) error {
	if args.Space != "" {
		space, err := client.ResolveSpace(ctx, args.Space)
		if err != nil {
			return err
		}
		if option.IOAJSON {
			return writeJSONOutput(stdout, space.Nodes)
		}
		if len(space.Nodes) == 0 {
			fmt.Fprintf(stderr, "no nodes in space %q\n", space.Name)
			return nil
		}
		w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
		fmt.Fprintf(w, "ID\tNAME\tDESCRIPTION\n")
		for _, n := range space.Nodes {
			fmt.Fprintf(w, "%s\t%s\t%s\n", n.ID, n.Name, n.Description)
		}
		return w.Flush()
	}

	nodes, err := client.ListNodes(ctx)
	if err != nil {
		return err
	}
	if option.IOAJSON {
		return writeJSONOutput(stdout, nodes)
	}
	if len(nodes) == 0 {
		fmt.Fprintln(stderr, "no nodes found")
		return nil
	}
	w := tabwriter.NewWriter(stdout, 0, 4, 2, ' ', 0)
	fmt.Fprintf(w, "ID\tNAME\n")
	for _, n := range nodes {
		fmt.Fprintf(w, "%s\t%s\n", n.ID, n.Name)
	}
	return w.Flush()
}

// ---------------------------------------------------------------------------
// REPL-boxed listings
//
// The CLI (cmd/aiscan) keeps the plain tabwriter tables above for scriptable,
// pipeable --json output. The interactive REPL renders the same data as boxed
// panels so /spaces, /nodes and /messages match /status and /provider.
// ---------------------------------------------------------------------------

func shortID(id string) string { return id[:min(len(id), 8)] }

func (r *AgentConsole) renderIOASpaces(ctx context.Context, client *ioaclient.Client) error {
	spaces, err := client.ListSpaces(ctx)
	if err != nil {
		return err
	}
	if len(spaces) == 0 {
		fmt.Fprintln(r.stderr, "no spaces found")
		return nil
	}
	rows := make([][]string, 0, len(spaces))
	for _, s := range spaces {
		rows = append(rows, []string{
			shortID(s.ID), s.Name,
			fmt.Sprintf("%d node", len(s.Nodes)), fmt.Sprintf("%d msg", s.MessageCount),
		})
	}
	r.printBoxTable("spaces", rows)
	return nil
}

func (r *AgentConsole) renderIOANodes(ctx context.Context, client *ioaclient.Client, space string) error {
	if space != "" {
		sp, err := client.ResolveSpace(ctx, space)
		if err != nil {
			return err
		}
		if len(sp.Nodes) == 0 {
			fmt.Fprintf(r.stderr, "no nodes in space %q\n", sp.Name)
			return nil
		}
		rows := make([][]string, 0, len(sp.Nodes))
		for _, n := range sp.Nodes {
			rows = append(rows, []string{shortID(n.ID), n.Name, n.Description})
		}
		r.printBoxTable("nodes", rows)
		return nil
	}
	nodes, err := client.ListNodes(ctx)
	if err != nil {
		return err
	}
	if len(nodes) == 0 {
		fmt.Fprintln(r.stderr, "no nodes found")
		return nil
	}
	rows := make([][]string, 0, len(nodes))
	for _, n := range nodes {
		rows = append(rows, []string{shortID(n.ID), n.Name})
	}
	r.printBoxTable("nodes", rows)
	return nil
}

func (r *AgentConsole) renderIOAMessages(ctx context.Context, client *ioaclient.Client, space string) error {
	sp, err := client.ResolveSpace(ctx, space)
	if err != nil {
		return err
	}
	messages, err := client.ReadPublic(ctx, sp.ID, protocols.ReadOptions{})
	if err != nil {
		return err
	}
	if len(messages) == 0 {
		fmt.Fprintf(r.stderr, "no start messages in space %q\n", sp.Name)
		return nil
	}
	rows := make([][]string, 0, len(messages))
	for _, m := range messages {
		rows = append(rows, []string{shortID(m.ID), m.Sender, contentPreview(m.Content, 48)})
	}
	r.printBoxTable("messages", rows)
	return nil
}

func (r *AgentConsole) printBoxTable(title string, rows [][]string) {
	colorEnabled := r.output != nil && r.output.color.Enabled
	fmt.Fprint(r.stdout, r.renderPanel(title, renderBoxTable(rows, colorEnabled), colorEnabled))
}

func contentPreview(content map[string]any, maxLen int) string {
	if text, ok := content["text"].(string); ok {
		if len(text) > maxLen {
			return text[:maxLen] + "..."
		}
		return text
	}
	data, _ := json.Marshal(content)
	s := string(data)
	if len(s) > maxLen {
		return s[:maxLen] + "..."
	}
	return s
}

func writeJSONOutput(w io.Writer, v any) error {
	enc := json.NewEncoder(w)
	enc.SetIndent("", "  ")
	return enc.Encode(v)
}
