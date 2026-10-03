package testutil

import (
	"context"
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
	ctx := context.Background()
	// copy env so the caller's map isn't changed
	env = maps.Clone(env)
	if env == nil {
		env = map[string]string{}
	}
	stdin := strings.NewReader(input)
	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
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
