package client

import (
	"context"
	"fmt"
	consoleapi "github.com/chainreactors/cyber/pkg/console/api"
	ioatools "github.com/chainreactors/cyber/tools/ioa"
	"github.com/spf13/cobra"
	"strings"
)

func ConsoleBindings(reader ioatools.Reader, space, endpoint string) *consoleapi.Bindings {
	endpoint = redactIOAURL(endpoint)
	return &consoleapi.Bindings{
		Commands: func(view consoleapi.View) []*cobra.Command { return commandBindings(reader, view) },
		Complete: func(ctx context.Context, value string) []string {
			value = strings.TrimPrefix(value, "@")
			var names []string
			if space != "" {
				info, err := reader.ResolveSpace(ctx, space)
				if err == nil {
					for _, node := range info.Nodes {
						if strings.HasPrefix(node.Name, value) {
							names = append(names, "@"+node.Name)
						}
					}
					return names
				}
			}
			nodes, err := reader.ListNodes(ctx)
			if err != nil {
				return nil
			}
			for _, node := range nodes {
				if strings.HasPrefix(node.Name, value) {
					names = append(names, "@"+node.Name)
				}
			}
			return names
		},
		Status: func() []consoleapi.Row {
			return []consoleapi.Row{{Name: "server", Value: endpoint}}
		},
	}
}
func commandBindings(reader ioatools.Reader, view consoleapi.View) []*cobra.Command {
	r := &presentation{view: view, stdout: view.Out, stderr: view.Err}
	return []*cobra.Command{
		{
			Use: "/spaces", Short: "List all spaces",
			Args: cobra.NoArgs,
			RunE: func(c *cobra.Command, _ []string) error {
				ctx := c.Context()
				client := reader
				return r.renderIOASpaces(ctx, client)
			},
		},
		{
			Use: "/messages", Short: "List start messages in a space",
			Args: cobra.ExactArgs(1), DisableFlagParsing: true,
			RunE: func(c *cobra.Command, args []string) error {
				ctx := c.Context()
				client := reader
				return r.renderIOAMessages(ctx, client, args[0])
			},
		},
		{
			Use: "/context", Short: "View message thread/context", Args: cobra.ExactArgs(2),
			DisableFlagParsing: true,
			RunE: func(c *cobra.Command, args []string) error {
				ctx := c.Context()
				fields := args
				if len(fields) < 2 {
					return fmt.Errorf("usage: /context <space> <message-id>")
				}
				client := reader
				return RunIOAContext(ctx, client, &ConsoleOptions{}, ConsoleArgs{Space: fields[0], MessageID: fields[1]}, r.stdout, r.stderr)
			},
		},
		{
			Use: "/nodes", Short: "List nodes (optionally scoped to a space)", Args: cobra.MaximumNArgs(1),
			DisableFlagParsing: true,
			RunE: func(c *cobra.Command, args []string) error {
				ctx := c.Context()
				client := reader
				space := ""
				if len(args) > 0 {
					space = args[0]
				}
				return r.renderIOANodes(ctx, client, space)
			},
		},
	}
}
