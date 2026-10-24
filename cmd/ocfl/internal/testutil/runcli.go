package testutil

import (
	"context"
	"fmt"
	"io"
	"maps"
	"strings"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/run"
)

// RunCLI runs the ocfl command with args and the environment variables in env,
// then calls expect with the result.
func RunCLI(args []string, env map[string]string, expect func(err error, stdout, stderr string)) {
	RunCLIInput(args, env, "", expect)
}

// RunCLIInput is like RunCLI, with input as stdin.
func RunCLIInput(args []string, env map[string]string, input string, expect func(err error, stdout, stderr string)) {
	runCLI(context.Background(), args, env, input, &strings.Builder{}, expect)
}

// RunCLIInterrupt is like RunCLI, but it cancels the command's context, as an
// interrupt signal does, as soon as the command writes text containing match
// to stderr (for example, a log message).
func RunCLIInterrupt(args []string, env map[string]string, match string, expect func(err error, stdout, stderr string)) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	RunCLIOnLog(ctx, args, env, match, cancel, expect)
}

// RunCLIOnLog is like RunCLI, with the context ctx, but it calls fn, once, as
// soon as the command writes text containing match to stderr (for example, a
// log message). The command waits for fn to return.
func RunCLIOnLog(ctx context.Context, args []string, env map[string]string, match string, fn func(), expect func(err error, stdout, stderr string)) {
	stderr := &matchWriter{match: match, fn: fn}
	runCLI(ctx, args, env, "", stderr, expect)
}

// matchWriter calls fn, once, when text containing match is written to it.
type matchWriter struct {
	strings.Builder
	match string
	fn    func()
	done  bool
}

func (w *matchWriter) Write(p []byte) (int, error) {
	n, err := w.Builder.Write(p)
	if !w.done && strings.Contains(w.String(), w.match) {
		w.done = true
		w.fn()
	}
	return n, err
}

// stringWriter is a writer that returns what was written as a string, like
// *strings.Builder.
type stringWriter interface {
	io.Writer
	fmt.Stringer
}

func runCLI(ctx context.Context, args []string, env map[string]string, input string, stderr stringWriter, expect func(err error, stdout, stderr string)) {
	// copy env so the caller's map isn't changed
	env = maps.Clone(env)
	if env == nil {
		env = map[string]string{}
	}
	stdin := strings.NewReader(input)
	stdout := &strings.Builder{}
	getenv := func(key string) string { return env[key] }
	args = append([]string{"ocfl"}, args...)
	// configure s3 test endpoint if enabled
	if S3Enabled() {
		env["AWS_ENDPOINT_URL"] = S3Endpoint()
		env["OCFL_S3_PATHSTYLE"] = "true"
	}
	err := run.CLI(ctx, args, stdin, stdout, stderr, getenv)
	expect(err, stdout.String(), stderr.String())
}
