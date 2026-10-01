package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/run"
)

func main() { os.Exit(realMain()) }

// realMain is separate from main so that deferred cleanup runs before os.Exit.
// Interrupt and termination signals cancel the context so the CLI can return
// normally and close its storage backends.
func realMain() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := run.CLI(ctx, os.Args, os.Stdin, os.Stdout, os.Stderr, os.Getenv); err != nil {
		return 1
	}
	return 0
}
