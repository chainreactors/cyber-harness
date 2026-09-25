package main

import (
	"context"
	"errors"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/chainreactors/cyber/audit/internal/app"
	flags "github.com/jessevdk/go-flags"
)

func main() {
	ctx, cancel := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer cancel()
	if err := app.Run(ctx, os.Args[1:], os.Stdout, os.Stderr); err != nil {
		var flagErr *flags.Error
		if errors.As(err, &flagErr) && flagErr.Type == flags.ErrHelp {
			return
		}
		fmt.Fprintf(os.Stderr, "cyber-audit: %v\n", err)
		os.Exit(1)
	}
}
