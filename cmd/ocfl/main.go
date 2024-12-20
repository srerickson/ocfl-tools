package main

import (
	"context"
	"os"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/run"
)

func main() {
	ctx := context.Background()
	if err := run.CLI(ctx, os.Args, os.Stdin, os.Stdout, os.Stderr, os.Getenv); err != nil {
		os.Exit(1)
	}
}
