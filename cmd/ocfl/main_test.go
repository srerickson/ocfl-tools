//go:build unix

package main

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"syscall"
	"testing"
	"time"

	"github.com/carlmjohnson/be"
)

// helperEnv selects the behavior of the test binary when it is re-executed
// as a helper process by startHelper.
const helperEnv = "OCFL_TEST_SIGNAL_HELPER"

func TestMain(m *testing.M) {
	switch os.Getenv(helperEnv) {
	case "":
		os.Exit(m.Run())
	case "graceful":
		// returns as soon as the context is canceled
		os.Exit(interruptible(io.Discard, func(ctx context.Context) error {
			fmt.Println("ready")
			<-ctx.Done()
			return ctx.Err()
		}))
	case "stuck":
		// ignores cancellation, like a command that never checks its context
		os.Exit(interruptible(io.Discard, func(ctx context.Context) error {
			fmt.Println("ready")
			<-ctx.Done()
			fmt.Println("canceled")
			select {}
		}))
	}
}

// startHelper re-executes the test binary in the given helper mode and returns
// it along with a function that waits for the next line of its output.
func startHelper(t *testing.T, mode string) (*exec.Cmd, func(string)) {
	t.Helper()
	cmd := exec.Command(os.Args[0])
	cmd.Env = append(os.Environ(), helperEnv+"="+mode)
	stdout, err := cmd.StdoutPipe()
	be.NilErr(t, err)
	be.NilErr(t, cmd.Start())
	t.Cleanup(func() { _ = cmd.Process.Kill() }) // fails if it already exited
	lines := bufio.NewScanner(stdout)
	expect := func(want string) {
		t.Helper()
		got := make(chan string, 1)
		go func() {
			if lines.Scan() {
				got <- lines.Text()
			}
			close(got)
		}()
		select {
		case line := <-got:
			be.Equal(t, want, line)
		case <-time.After(10 * time.Second):
			t.Fatalf("timed out waiting for %q from helper", want)
		}
	}
	return cmd, expect
}

// exitCode waits for cmd and returns its exit code or, if it was killed by a
// signal, the signal.
func exitCode(t *testing.T, cmd *exec.Cmd) (code int, sig syscall.Signal) {
	t.Helper()
	done := make(chan error, 1)
	go func() { done <- cmd.Wait() }()
	var err error
	select {
	case err = <-done:
	case <-time.After(10 * time.Second):
		t.Fatal("timed out waiting for helper to exit")
	}
	var exitErr *exec.ExitError
	if err != nil && !errors.As(err, &exitErr) {
		t.Fatal(err)
	}
	status := cmd.ProcessState.Sys().(syscall.WaitStatus)
	if status.Signaled() {
		return -1, status.Signal()
	}
	return status.ExitStatus(), 0
}

func TestInterruptible(t *testing.T) {
	t.Run("success", func(t *testing.T) {
		code := interruptible(io.Discard, func(context.Context) error { return nil })
		be.Equal(t, 0, code)
	})
	t.Run("error", func(t *testing.T) {
		code := interruptible(io.Discard, func(context.Context) error { return errors.New("fail") })
		be.Equal(t, 1, code)
	})
	for _, tc := range []struct {
		sig  syscall.Signal
		code int
	}{
		{syscall.SIGINT, 130},
		{syscall.SIGTERM, 143},
	} {
		t.Run(tc.sig.String()+" cancels context", func(t *testing.T) {
			cmd, expect := startHelper(t, "graceful")
			expect("ready")
			be.NilErr(t, cmd.Process.Signal(tc.sig))
			code, sig := exitCode(t, cmd)
			be.Equal(t, syscall.Signal(0), sig)
			be.Equal(t, tc.code, code)
		})
	}
	t.Run("second signal force quits", func(t *testing.T) {
		cmd, expect := startHelper(t, "stuck")
		expect("ready")
		be.NilErr(t, cmd.Process.Signal(syscall.SIGINT))
		expect("canceled")
		be.NilErr(t, cmd.Process.Signal(syscall.SIGINT))
		_, sig := exitCode(t, cmd)
		be.Equal(t, syscall.SIGINT, sig)
	})
}
