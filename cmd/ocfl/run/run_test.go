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
	contentFixture = filepath.Join(`..`, `..`, `..`, `testdata`, `content-fixture`)
	allLayouts     = []string{
		"0002-flat-direct-storage-layout",
		"0003-hash-and-id-n-tuple-storage-layout",
		"0004-hashed-n-tuple-storage-layout",
		// "0006-flat-omit-prefix-storage-layout",
		"0007-n-tuple-omit-prefix-storage-layout",
	}
)

func runCLI(args []string, env map[string]string, expect func(err error, stdout, stderr string)) {
	ctx := context.Background()
	stdout := &strings.Builder{}
	stderr := &strings.Builder{}
	getenv := func(key string) string { return env[key] }
	args = append([]string{"ocfl"}, args...)
	// configure s3 test endpoint if enabled
	if env != nil && testutil.S3Enabled() {
		env["AWS_ENDPOINT_URL"] = testutil.S3Endpoint()
	}
	err := run.CLI(ctx, args, stdout, stderr, getenv)
	expect(err, stdout.String(), stderr.String())
}

func TestInitRoot(t *testing.T) {
	t.Run("root not set", func(t *testing.T) {
		// should return an error
		args := []string{"init-root"}
		runCLI(args, nil, func(err error, stdout string, stderr string) {
			be.True(t, err != nil)
		})
	})
}

func TestAllLayouts(t *testing.T) {
	testLayout := func(t *testing.T, root string, layout string) {
		env := map[string]string{"OCFL_ROOT": root}
		rootDesc := "test description"
		args := []string{
			"init-root",
			"--description", rootDesc,
			"--layout", layout,
		}
		runCLI(args, env, func(err error, stdout string, stderr string) {
			be.NilErr(t, err)
			be.True(t, strings.Contains(stdout, root))
			be.True(t, strings.Contains(stdout, layout))
			be.True(t, strings.Contains(stdout, rootDesc))
		})
		// ocfl commit
		objID := "object-01"
		args = []string{
			"commit",
			contentFixture,
			"--id", objID,
			"--message", "my message",
			"--name", "Me",
			"--email", "me@domain.net",
		}
		runCLI(args, env, func(err error, _ string, _ string) {
			be.NilErr(t, err)
		})
		// ocfl ls
		args = []string{
			"ls",
			"--id", objID,
		}
		runCLI(args, env, func(err error, stdout string, _ string) {
			be.NilErr(t, err)
			be.True(t, strings.Contains(stdout, "hello.csv"))
			be.True(t, strings.Contains(stdout, "folder1/file.txt"))
		})
	}

	for _, l := range allLayouts {
		t.Run(l, func(t *testing.T) {
			testLayout(t, t.TempDir(), l)
			// again with S3 if enabled
			if testutil.S3Enabled() {
				t.Run("s3", func(t *testing.T) {
					testLayout(t, testutil.TempS3Location(t, "new-root"), l)
				})
			}
		})
	}
}
