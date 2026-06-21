//go:build ignore

// pw_driver is a persistent driver for the playwright pseudo-command.
// It reads JSON-line commands from stdin and writes JSON-line responses to stdout.
// The Command instance (and its sessions) persist across calls.
//
// Build: go build -tags browser -o pw_driver ./pkg/tools/playwright/testharness/pw_driver.go
//
// Protocol:
//   Input (one JSON per line):  {"args": ["open", "http://...", "--session", "s1"]}
//   Output (one JSON per line): {"output": "Session: s1\n...", "error": ""}
//
// Send {"args": ["__quit__"]} to exit cleanly.
package main

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/signal"

	"github.com/chainreactors/aiscan/pkg/commands"
	"github.com/chainreactors/aiscan/pkg/tools/playwright"
)

type request struct {
	Args []string `json:"args"`
}

type response struct {
	Output string `json:"output"`
	Error  string `json:"error,omitempty"`
}

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt)
	defer cancel()

	cmd := playwright.New(".")
	defer cmd.Close()

	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1<<20), 1<<20)
	enc := json.NewEncoder(os.Stdout)

	for scanner.Scan() {
		var req request
		if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
			_ = enc.Encode(response{Error: fmt.Sprintf("invalid JSON: %v", err)})
			continue
		}
		if len(req.Args) > 0 && req.Args[0] == "__quit__" {
			break
		}

		commands.Output.Reset(nil)
		err := cmd.Execute(ctx, req.Args)
		resp := response{Output: commands.Output.Captured()}
		if err != nil {
			resp.Error = err.Error()
		}
		_ = enc.Encode(resp)
	}
}
