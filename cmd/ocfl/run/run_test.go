package run_test

import (
	"context"
	"path/filepath"
	"strings"
	"testing"

	"github.com/carlmjohnson/be"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/internal/testutil"
	"github.com/srerickson/ocfl-tools/cmd/ocfl/run"
)

var (
	testDataPath       = filepath.Join(`..`, `..`, `..`, `testdata`)
	goodObjectFixtures = filepath.Join(testDataPath, `object-fixtures`, `1.1`, `good-objects`)
	badObjectFixtures  = filepath.Join(testDataPath, `object-fixtures`, `1.1`, `bad-objects`)
	goodStoreFixtures  = filepath.Join(testDataPath, `store-fixtures`, `1.0`, `good-stores`)
	badStoreFixtures   = filepath.Join(testDataPath, `store-fixtures`, `1.0`, `bad-stores`)
	contentFixture     = filepath.Join(testDataPath, `content-fixture`)
)

func runCLI(args []string, env map[string]string, expect func(err error, stdout, stderr string)) {
	ctx := context.Background()
	if env == nil {
		env = map[string]string{}
	}
	stdin := strings.NewReader("")
	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	getenv := func(key string) string { return env[key] }
	args = append([]string{"ocfl"}, args...)
	// configure s3 test endpoint if enabled
	if testutil.S3Enabled() {
		env["AWS_ENDPOINT_URL"] = testutil.S3Endpoint()
		env["OCFL_S3_PATHSTYLE"] = "true"
	}
	err := run.CLI(ctx, args, stdin, stdout, stderr, getenv)
	expect(err, stdout.String(), stderr.String())
}

func TestRootNotSet(t *testing.T) {
	// test commands fail if root is not set
	cmds := []string{"init-root", "ls", "commit", "validate", "export", "diff"}
	for _, cmd := range cmds {
		// should return an error
		args := []string{cmd}
		runCLI(args, nil, func(err error, stdout string, stderr string) {
			be.True(t, err != nil)
		})
	}
}
