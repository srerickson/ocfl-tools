package main

import (
	"context"
	"os"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/run"
)

func main() {
	ctx := context.Background()
	err := run.CLI(ctx, os.Args, os.Stdin, os.Stdout, os.Stderr, os.Getenv)
	if err != nil {
		os.Exit(1)
	}
}
