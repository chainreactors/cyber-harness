package main

import (
	"context"
	"flag"
	"fmt"
	"os"
	"strings"
	"time"
)

func main() {
	baseURL := flag.String("url", envOr("AISCAN_WEB_URL", "http://127.0.0.1:8080"), "AIScan Web base URL")
	token := flag.String("token", os.Getenv("AISCAN_WEB_TOKEN"), "Web access token (or AISCAN_WEB_TOKEN)")
	agentID := flag.String("agent", "", "connected agent ID; empty selects an LLM-capable agent")
	prompt := flag.String("prompt", "", "natural-language input")
	timeout := flag.Duration("timeout", 10*time.Minute, "maximum time to wait for turn_ended")
	stream := flag.Bool("stream", false, "print text deltas while the agent runs")
	flag.Parse()

	input := strings.TrimSpace(*prompt)
	if input == "" {
		input = strings.TrimSpace(strings.Join(flag.Args(), " "))
	}
	if input == "" {
		fmt.Fprintln(os.Stderr, "usage: go run ./examples/web-chat -prompt \"summarize the authorized target\"")
		os.Exit(2)
	}
	client, err := NewClient(*baseURL, *token)
	if err != nil {
		fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), *timeout)
	defer cancel()

	printedDelta := false
	var onDelta func(string)
	if *stream {
		onDelta = func(delta string) {
			printedDelta = true
			fmt.Print(delta)
		}
	}
	result, err := client.Ask(ctx, input, *agentID, onDelta)
	if err != nil {
		fatal(err)
	}
	if printedDelta {
		fmt.Println()
	} else {
		fmt.Println(result.Output)
	}
	fmt.Fprintf(os.Stderr, "session=%s agent=%s stop=%s\n", result.SessionID, result.AgentID, result.Stop)
}

func envOr(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}

func fatal(err error) {
	fmt.Fprintln(os.Stderr, "error:", err)
	os.Exit(1)
}
