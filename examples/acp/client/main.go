package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"os/signal"
	"strings"
	"syscall"

	aop "github.com/chainreactors/aiscan/aop"
	coretool "github.com/chainreactors/aiscan/core/tool"
)

// acp client: send one natural-language prompt to an aiscan headless server
// and stream the agent's events back to stdout.
//
//	go run ./examples/acp/client --server http://127.0.0.1:8080 --token <key> --node local -p "list files with bash"
func main() {
	var (
		serverURL string
		token     string
		nodeID    string
		prompt    string
		title     string
	)
	flag.StringVar(&serverURL, "server", "", "aiscan server URL, e.g. http://127.0.0.1:8080")
	flag.StringVar(&token, "token", "", "server access token")
	flag.StringVar(&nodeID, "node", "", "agent node ID to open the session on")
	flag.StringVar(&prompt, "p", "", "natural-language prompt for the turn")
	flag.StringVar(&title, "title", "", "session title")
	flag.Parse()
	if serverURL == "" || nodeID == "" || prompt == "" {
		fmt.Fprintln(os.Stderr, "usage: acp-client --server <url> --node <node-id> -p <prompt> [--token <key>]")
		os.Exit(2)
	}

	ctx, cancel := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer cancel()

	client, err := Dial(ctx, serverURL, "", token)
	if err != nil {
		fmt.Fprintf(os.Stderr, "dial: %v\n", err)
		os.Exit(1)
	}
	defer client.Close()

	session, err := client.OpenSession(ctx, nodeID, title)
	if err != nil {
		fmt.Fprintf(os.Stderr, "open session: %v\n", err)
		os.Exit(1)
	}
	fmt.Fprintf(os.Stderr, "session %s on node %s\n", session.GetId(), session.GetNodeId())

	events, err := client.Watch(session.GetId(), "")
	if err != nil {
		fmt.Fprintf(os.Stderr, "watch: %v\n", err)
		os.Exit(1)
	}

	if _, err := client.RunTurn(ctx, session.GetId(), prompt); err != nil {
		fmt.Fprintf(os.Stderr, "run turn: %v\n", err)
		os.Exit(1)
	}

	for event := range events {
		if printEvent(event) {
			return
		}
	}
}

// printEvent renders one event; it returns true when the turn is over.
func printEvent(event *aop.Event) bool {
	switch payload := event.GetPayload().(type) {
	case *aop.Event_MessageDelta:
		if text := payload.MessageDelta.GetText(); text != "" {
			fmt.Print(text)
		}
	case *aop.Event_ToolCall:
		fmt.Printf("\n→ tool %s\n", payload.ToolCall.GetName())
	case *aop.Event_ToolResult:
		out := strings.TrimSpace(coretool.ResultText(payload.ToolResult))
		if len(out) > 200 {
			out = out[:200] + "…"
		}
		fmt.Printf("← %s\n", out)
	case *aop.Event_TurnEnded:
		fmt.Println()
		if err := payload.TurnEnded.GetError(); err != nil {
			fmt.Fprintf(os.Stderr, "turn failed %s: %s\n", err.GetCode(), err.GetMessage())
		}
		return true
	case *aop.Event_SessionEnded:
		return true
	case *aop.Event_Error:
		fmt.Fprintf(os.Stderr, "error %s: %s\n", payload.Error.GetCode(), payload.Error.GetMessage())
	}
	return false
}
