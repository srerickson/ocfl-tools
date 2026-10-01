package main

import (
	"context"
	"fmt"
	"io"
	"os"
	"os/signal"
	"sync/atomic"
	"syscall"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/run"
)

func main() {
	os.Exit(interruptible(os.Stderr, func(ctx context.Context) error {
		return run.CLI(ctx, os.Args, os.Stdin, os.Stdout, os.Stderr, os.Getenv)
	}))
}

// interruptible runs fn and returns the process exit code: 0 if fn succeeds
// and 1 if it fails. It is separate from main so that fn's deferred cleanup
// (closing storage backends) runs before os.Exit.
//
// The first interrupt or termination signal cancels the context passed to fn
// and restores the default signal behavior, so fn can stop cleanly but a
// second signal terminates the process immediately. If a signal was received,
// the exit code is 128 plus the signal number (130 for SIGINT), as for a
// process killed by that signal.
func interruptible(stderr io.Writer, fn func(context.Context) error) int {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	sigs := make(chan os.Signal, 1)
	signal.Notify(sigs, os.Interrupt, syscall.SIGTERM)
	defer signal.Stop(sigs)
	var received atomic.Value // os.Signal
	go func() {
		select {
		case sig := <-sigs:
			// stop before canceling: once fn sees the cancellation, another
			// signal must get the default behavior.
			signal.Stop(sigs)
			received.Store(sig)
			fmt.Fprintf(stderr, "received %s: stopping (repeat to force quit)\n", sig)
			cancel()
		case <-ctx.Done():
		}
	}()
	err := fn(ctx)
	if sig, ok := received.Load().(syscall.Signal); ok {
		return 128 + int(sig)
	}
	if err != nil {
		return 1
	}
	return 0
}
