package testutil

import (
	"context"
	"strings"

	"github.com/srerickson/ocfl-tools/cmd/ocfl/run"
)

func RunCLI(args []string, env map[string]string, expect func(err error, stdout, stderr string)) {
	RunCLIInput(args, env, "", expect)
}

func RunCLIInput(args []string, env map[string]string, input string, expect func(err error, stdout, stderr string)) {
	ctx := context.Background()
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
